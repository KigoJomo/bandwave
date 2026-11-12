package main

import (
	"context"
	"log"
	"time"
)

// Capturer delivers mono float32 samples of whatever the machine is playing.
type Capturer interface {
	// Run blocks, calling onSamples with mono samples, until ctx ends or capture fails.
	Run(ctx context.Context, onSamples func([]float32)) error
	Rate() int
}

const fps = 60

// LocalSource captures this machine's output and publishes Frames.
type LocalSource struct {
	frames chan Frame
}

// StartLocal begins capture in the background, restarting it if it fails
// (e.g. the default output device changed or the sound server restarted).
func StartLocal(ctx context.Context, device string) *LocalSource {
	ls := &LocalSource{frames: make(chan Frame, 4)}
	go func() {
		for ctx.Err() == nil {
			c := newCapturer(device)
			an := NewAnalyzer(c.Rate())
			cctx, cancel := context.WithCancel(ctx)
			ticked := make(chan struct{})
			go func() {
				ls.tick(cctx, an, c.Rate())
				close(ticked)
			}()
			err := c.Run(cctx, an.Write)
			cancel()
			<-ticked // never close frames while the ticker may still send
			if ctx.Err() != nil {
				break
			}
			if err != nil {
				log.Printf("capture: %v (retrying)", err)
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
			}
		}
		close(ls.frames)
	}()
	return ls
}

func (ls *LocalSource) tick(ctx context.Context, an *Analyzer, rate int) {
	t := time.NewTicker(time.Second / fps)
	defer t.Stop()
	hop := rate / fps
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f := an.Frame(hop)
			select {
			case ls.frames <- f:
			default: // consumer is slow; drop
			}
		}
	}
}

func (ls *LocalSource) Frames() <-chan Frame { return ls.frames }

func toMonoF32(dst []float32, interleaved []float32, ch int) []float32 {
	dst = dst[:0]
	if ch < 1 {
		ch = 1
	}
	for i := 0; i+ch <= len(interleaved); i += ch {
		var s float32
		for c := 0; c < ch; c++ {
			s += interleaved[i+c]
		}
		dst = append(dst, s/float32(ch))
	}
	return dst
}

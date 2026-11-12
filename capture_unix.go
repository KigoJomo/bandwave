//go:build !windows

package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os/exec"
)

// pulseCapturer reads the default sink's monitor through parec (works on
// PulseAudio and PipeWire's pulse server), falling back to pw-record.
type pulseCapturer struct{ device string }

func newCapturer(device string) Capturer { return &pulseCapturer{device: device} }

func (p *pulseCapturer) Rate() int { return 48000 }

func (p *pulseCapturer) cmd(ctx context.Context) (*exec.Cmd, error) {
	if path, err := exec.LookPath("parec"); err == nil {
		dev := p.device
		if dev == "" {
			dev = "@DEFAULT_MONITOR@"
		}
		return exec.CommandContext(ctx, path, "--raw", "--device="+dev, "--format=float32le",
			"--rate=48000", "--channels=2", "--latency-msec=10", "--client-name=bandwave"), nil
	}
	if path, err := exec.LookPath("pw-record"); err == nil {
		args := []string{"--format=f32", "--rate=48000", "--channels=2", "--latency=10ms", "-P", "stream.capture.sink=true"}
		if p.device != "" {
			args = append(args, "--target="+p.device)
		}
		return exec.CommandContext(ctx, path, append(args, "-")...), nil
	}
	return nil, fmt.Errorf("need parec (libpulse) or pw-record (pipewire) to capture audio")
}

func (p *pulseCapturer) Run(ctx context.Context, onSamples func([]float32)) error {
	cmd, err := p.cmd(ctx)
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	defer cmd.Wait()
	r := bufio.NewReaderSize(out, 1<<14)
	raw := make([]byte, 2*4*256) // 256 stereo frames
	st := make([]float32, 512)
	mono := make([]float32, 0, 256)
	for {
		if _, err := io.ReadFull(r, raw); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("%s stopped: %w", cmd.Path, err)
		}
		for i := range st {
			st[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
		}
		mono = toMonoF32(mono, st, 2)
		onSamples(mono)
	}
}

package main

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"runtime"
	"sync"
	"time"
	"unicode"
)

// Wire protocol (TCP, default port 47630):
//
//	client -> "BANDWAVE/1\n"
//	server -> one JSON line: Hello
//	server -> frames: uint32 BE length, then payload =
//	          float32 level, NumBands x float32 bands (all little-endian)
//
// Only analysed band magnitudes cross the network, never audio.
const (
	DefaultPort  = 47630
	BeaconPort   = 47631
	protoMagic   = "BANDWAVE/1\n"
	frameBytes   = 4 + NumBands*4
	silenceLevel = 0.0005
)

type Hello struct {
	Name    string `json:"name"`
	OS      string `json:"os"`
	Version string `json:"version"`
	Bands   int    `json:"bands"`
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

func encodeFrame(dst []byte, f Frame) []byte {
	dst = binary.BigEndian.AppendUint32(dst[:0], frameBytes)
	dst = binary.LittleEndian.AppendUint32(dst, math.Float32bits(f.Level))
	for _, b := range f.Bands {
		dst = binary.LittleEndian.AppendUint32(dst, math.Float32bits(b))
	}
	return dst
}

func decodeFrame(p []byte) (Frame, error) {
	var f Frame
	if len(p) != frameBytes {
		return f, fmt.Errorf("bad frame size %d", len(p))
	}
	f.Level = sane(math.Float32frombits(binary.LittleEndian.Uint32(p)))
	for i := range f.Bands {
		f.Bands[i] = sane(math.Float32frombits(binary.LittleEndian.Uint32(p[4+i*4:])))
	}
	return f, nil
}

// sane maps NaN, infinities and negatives from a broken peer to 0 so they
// can't poison the smoothing state.
func sane(v float32) float32 {
	if v != v || math.IsInf(float64(v), 0) || v < 0 || v > 1e6 {
		return 0
	}
	return v
}

// cleanText keeps remote names printable so a peer can't send terminal
// escape sequences to the viewer.
func cleanText(s string, n int) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if len(out) == n {
			break
		}
		if unicode.IsPrint(r) {
			out = append(out, r)
		}
	}
	return string(out)
}

// Server fans out the local source's frames to every connected viewer.
// While the machine is silent it only sends a few frames a second.
type Server struct {
	mu      sync.Mutex
	clients map[chan Frame]struct{}
}

func Serve(ctx context.Context, listen string, src *LocalSource, beacon bool) error {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	log.Printf("bandwave agent %s listening on %s", hostname(), ln.Addr())
	s := &Server{clients: map[chan Frame]struct{}{}}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	go s.fanout(src)
	if beacon {
		go runBeacon(ctx, ln.Addr().(*net.TCPAddr).Port)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("accept: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go s.handle(ctx, c)
	}
}

func (s *Server) fanout(src *LocalSource) {
	quietSince := time.Now()
	var lastSent time.Time
	for f := range src.Frames() {
		now := time.Now()
		if f.Level > silenceLevel {
			quietSince = now
		}
		if now.Sub(quietSince) > 2*time.Second && now.Sub(lastSent) < 200*time.Millisecond {
			continue
		}
		lastSent = now
		s.mu.Lock()
		for ch := range s.clients {
			select {
			case ch <- f:
			default:
			}
		}
		s.mu.Unlock()
	}
}

func (s *Server) handle(ctx context.Context, c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	magic := make([]byte, len(protoMagic))
	if _, err := io.ReadFull(c, magic); err != nil || string(magic) != protoMagic {
		return
	}
	hello, _ := json.Marshal(Hello{Name: hostname(), OS: runtime.GOOS, Version: version, Bands: NumBands})
	if _, err := c.Write(append(hello, '\n')); err != nil {
		return
	}
	ch := make(chan Frame, 8)
	s.mu.Lock()
	s.clients[ch] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, ch)
		s.mu.Unlock()
	}()
	// Detect the viewer going away even when we are not writing.
	done := make(chan struct{})
	go func() {
		c.SetReadDeadline(time.Time{})
		io.Copy(io.Discard, c)
		close(done)
	}()
	buf := make([]byte, 0, 4+frameBytes)
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case f := <-ch:
			c.SetWriteDeadline(time.Now().Add(3 * time.Second))
			buf = encodeFrame(buf, f)
			if _, err := c.Write(buf); err != nil {
				return
			}
		}
	}
}

// Dial connects to an agent and streams its frames until ctx ends or the
// connection drops.
func Dial(ctx context.Context, addr string, onHello func(Hello), onFrame func(Frame)) error {
	d := net.Dialer{Timeout: 3 * time.Second}
	c, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer c.Close()
	go func() {
		<-ctx.Done()
		c.Close()
	}()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, protoMagic); err != nil {
		return err
	}
	r := bufio.NewReaderSize(c, 4096)
	line, err := r.ReadSlice('\n') // fails with ErrBufferFull past 4 KiB
	if err != nil {
		return fmt.Errorf("handshake: %w", err)
	}
	var h Hello
	if err := json.Unmarshal(line, &h); err != nil {
		return fmt.Errorf("handshake: %w", err)
	}
	if h.Bands != NumBands {
		return fmt.Errorf("%s speaks %d bands, we need %d (version mismatch?)", addr, h.Bands, NumBands)
	}
	h.Name, h.OS = cleanText(h.Name, 40), cleanText(h.OS, 12)
	onHello(h)
	hdr := make([]byte, 4)
	p := make([]byte, frameBytes)
	for {
		c.SetReadDeadline(time.Now().Add(5 * time.Second)) // agents send >= 5 fps
		if _, err := io.ReadFull(r, hdr); err != nil {
			return err
		}
		if binary.BigEndian.Uint32(hdr) != frameBytes {
			return fmt.Errorf("bad frame length")
		}
		if _, err := io.ReadFull(r, p); err != nil {
			return err
		}
		f, err := decodeFrame(p)
		if err != nil {
			return err
		}
		onFrame(f)
	}
}

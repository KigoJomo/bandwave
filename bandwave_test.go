package main

import (
	"context"
	"math"
	"net"
	"strings"
	"testing"
	"time"
)

func bandFor(freq float64, rate int) int {
	top := math.Min(maxFreq, float64(rate)/2*0.95)
	return int(math.Log(freq/minFreq) / math.Log(top/minFreq) * NumBands)
}

func TestAnalyzerFindsTone(t *testing.T) {
	for _, freq := range []float64{60, 440, 1000, 8000} {
		rate := 48000
		a := NewAnalyzer(rate)
		s := make([]float32, fftSize)
		for i := range s {
			s[i] = float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(rate)))
		}
		a.Write(s)
		f := a.Frame(800)
		best := 0
		for i, v := range f.Bands {
			if v > f.Bands[best] {
				best = i
			}
		}
		if want := bandFor(freq, rate); best < want-1 || best > want+1 {
			t.Errorf("%v Hz: peak band %d, want ~%d", freq, best, want)
		}
		if math.Abs(float64(f.Level)-0.5/math.Sqrt2) > 0.01 {
			t.Errorf("%v Hz: level %v", freq, f.Level)
		}
	}
}

func TestFrameRoundTrip(t *testing.T) {
	var f Frame
	f.Level = 0.25
	for i := range f.Bands {
		f.Bands[i] = float32(i) / 7
	}
	buf := encodeFrame(nil, f)
	g, err := decodeFrame(buf[4:])
	if err != nil || g != f {
		t.Fatalf("round trip: %v %v", err, g == f)
	}
}

func TestServeAndDial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	src := &LocalSource{frames: make(chan Frame, 4)}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	go Serve(ctx, addr, src, false)
	go func() {
		var f Frame
		f.Level = 0.3
		f.Bands[10] = 1
		for ctx.Err() == nil {
			src.frames <- f
			time.Sleep(10 * time.Millisecond)
		}
	}()
	time.Sleep(100 * time.Millisecond)
	got := make(chan Frame, 1)
	var hello Hello
	go Dial(ctx, addr, func(h Hello) { hello = h }, func(f Frame) {
		select {
		case got <- f:
		default:
		}
	})
	select {
	case f := <-got:
		if f.Bands[10] != 1 || hello.Name == "" {
			t.Fatalf("bad frame/hello: %+v %+v", f.Level, hello)
		}
	case <-ctx.Done():
		t.Fatal("no frame received")
	}
}

func TestKDEConnectParse(t *testing.T) {
	line := "- studio-pc: 0123456789abcdef0123456789abcdef on 192.168.1.20 via LAN (paired and reachable)"
	m := kdcAddr.FindStringSubmatch(line)
	if m == nil || m[1] != "studio-pc" || m[2] != "192.168.1.20" {
		t.Fatalf("parse: %q", m)
	}
}

func TestDrawFits(t *testing.T) {
	var sb strings.Builder
	vals := []float64{0, 0.5, 1}
	Draw(&sb, vals, 20, 5, Style{BarWidth: 2, Gap: 1, Status: true}, "status")
	if n := strings.Count(sb.String(), "\r\n"); n != 4 {
		t.Fatalf("want 4 line breaks, got %d", n)
	}
}

func TestStatusTruncationIsRuneSafe(t *testing.T) {
	var sb strings.Builder
	st := Style{BarWidth: 2, Gap: 1, Status: true}
	status := " no source · auto · 0 source(s) · tab next · a auto · q quit"
	for w := 4; w < 80; w++ {
		Draw(&sb, []float64{0.5}, w, 4, st, status) // must not panic
	}
}

func TestPeerInputIsSanitised(t *testing.T) {
	if got := cleanText("evil\x1b]52;c;aGk=\x07name", 40); strings.ContainsAny(got, "\x1b\x07") {
		t.Fatalf("escape survived: %q", got)
	}
	if got := cleanText(strings.Repeat("a", 100), 40); len(got) != 40 {
		t.Fatalf("not truncated: %d", len(got))
	}
	var f Frame
	f.Bands[0] = float32(math.NaN())
	f.Bands[1] = float32(math.Inf(1))
	f.Bands[2] = -1
	g, _ := decodeFrame(encodeFrame(nil, f)[4:])
	if g.Bands[0] != 0 || g.Bands[1] != 0 || g.Bands[2] != 0 {
		t.Fatalf("bad values passed: %v", g.Bands[:3])
	}
}

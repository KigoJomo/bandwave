package main

import (
	"math"
	"math/cmplx"
	"sync"
)

// NumBands is the fixed number of log-spaced bands every source produces.
// Viewers resample these to however many bars fit the terminal, so local and
// remote sources look identical.
const NumBands = 96

const (
	fftSize = 4096
	minFreq = 40.0
	maxFreq = 16000.0
)

// Frame is one analysed slice of audio.
type Frame struct {
	Level float32           // RMS of the slice, 0..1
	Bands [NumBands]float32 // weighted band magnitudes (unnormalised)
}

// Analyzer turns a stream of mono samples into Frames.
type Analyzer struct {
	mu     sync.Mutex
	rate   int
	ring   []float32
	pos    int
	window []float64
	buf    []complex128
	edges  [NumBands + 1]float64 // band edges in (fractional) FFT bins
	weight [NumBands]float64
}

func NewAnalyzer(rate int) *Analyzer {
	a := &Analyzer{rate: rate, ring: make([]float32, fftSize), window: make([]float64, fftSize), buf: make([]complex128, fftSize)}
	for i := range a.window {
		a.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(fftSize-1))
	}
	top := math.Min(maxFreq, float64(rate)/2*0.95)
	binHz := float64(rate) / fftSize
	for i := 0; i <= NumBands; i++ {
		f := minFreq * math.Pow(top/minFreq, float64(i)/NumBands)
		a.edges[i] = f / binHz
	}
	for i := 0; i < NumBands; i++ {
		fc := minFreq * math.Pow(top/minFreq, (float64(i)+0.5)/NumBands)
		// Music energy falls roughly with frequency; tilt so treble is visible.
		a.weight[i] = math.Pow(fc/minFreq, 0.55)
	}
	return a
}

// Write appends samples to the analysis window.
func (a *Analyzer) Write(samples []float32) {
	a.mu.Lock()
	for _, s := range samples {
		a.ring[a.pos] = s
		a.pos = (a.pos + 1) % fftSize
	}
	a.mu.Unlock()
}

// Frame analyses the most recent fftSize samples. hop is how many of the newest
// samples to use for the level meter.
func (a *Analyzer) Frame(hop int) Frame {
	a.mu.Lock()
	var sq float64
	for i := 0; i < fftSize; i++ {
		s := float64(a.ring[(a.pos+i)%fftSize])
		a.buf[i] = complex(s*a.window[i], 0)
		if i >= fftSize-hop {
			sq += s * s
		}
	}
	a.mu.Unlock()

	fft(a.buf)
	var f Frame
	if hop > 0 {
		f.Level = float32(math.Sqrt(sq / float64(hop)))
	}
	half := fftSize / 2
	for b := 0; b < NumBands; b++ {
		lo, hi := a.edges[b], a.edges[b+1]
		var p float64
		if hi-lo < 1 {
			// Narrower than a bin: interpolate magnitude at the band centre.
			c := (lo + hi) / 2
			i := int(c)
			t := c - float64(i)
			if i+1 < half {
				p = (1-t)*cmplx.Abs(a.buf[i]) + t*cmplx.Abs(a.buf[i+1])
			}
		} else {
			n := 0
			for i := int(math.Ceil(lo)); i < int(math.Ceil(hi)) && i < half; i++ {
				m := cmplx.Abs(a.buf[i])
				p += m * m
				n++
			}
			if n > 0 {
				p = math.Sqrt(p / float64(n))
			}
		}
		f.Bands[b] = float32(p * a.weight[b] / fftSize)
	}
	return f
}

// fft is an in-place iterative radix-2 FFT; len(x) must be a power of two.
func fft(x []complex128) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		w := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			wn := complex(1, 0)
			for k := 0; k < size/2; k++ {
				u := x[start+k]
				v := x[start+k+size/2] * wn
				x[start+k] = u + v
				x[start+k+size/2] = u - v
				wn *= w
			}
		}
	}
}

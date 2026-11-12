//go:build windows

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/moutend/go-wca/pkg/wca"
)

// wasapiCapturer records the default render device via WASAPI loopback.
type wasapiCapturer struct {
	rate int
}

func newCapturer(device string) Capturer {
	_ = device // only the default output is supported on Windows for now
	return &wasapiCapturer{rate: mixRate()}
}

func (w *wasapiCapturer) Rate() int { return w.rate }

// mixRate asks the default output for its mix rate so the analyzer matches it.
func mixRate() int {
	rate := 48000
	_ = withClient(func(ac *wca.IAudioClient, wfx *wca.WAVEFORMATEX, _ func() bool) error {
		rate = int(wfx.NSamplesPerSec)
		return nil
	})
	return rate
}

// withClient opens the default output device on a locked, COM-initialised
// thread. fn gets a deviceChanged check that reports whether the default
// output is now a different device.
func withClient(fn func(ac *wca.IAudioClient, wfx *wca.WAVEFORMATEX, deviceChanged func() bool) error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		if oe, ok := err.(*ole.OleError); !ok || (oe.Code() != 1 && oe.Code() != 0) { // S_FALSE: already initialised
			return fmt.Errorf("CoInitializeEx: %w", err)
		}
	}
	defer ole.CoUninitialize()

	var mmde *wca.IMMDeviceEnumerator
	if err := wca.CoCreateInstance(wca.CLSID_MMDeviceEnumerator, 0, wca.CLSCTX_ALL, wca.IID_IMMDeviceEnumerator, &mmde); err != nil {
		return fmt.Errorf("device enumerator: %w", err)
	}
	defer mmde.Release()
	var mmd *wca.IMMDevice
	if err := mmde.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &mmd); err != nil {
		return fmt.Errorf("no default output device: %w", err)
	}
	defer mmd.Release()
	var id string
	mmd.GetId(&id)
	deviceChanged := func() bool {
		var now *wca.IMMDevice
		if err := mmde.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &now); err != nil {
			return true
		}
		defer now.Release()
		var nowID string
		now.GetId(&nowID)
		return nowID != id
	}
	var ac *wca.IAudioClient
	if err := mmd.Activate(wca.IID_IAudioClient, wca.CLSCTX_ALL, nil, &ac); err != nil {
		return fmt.Errorf("activate audio client: %w", err)
	}
	defer ac.Release()
	var wfx *wca.WAVEFORMATEX
	if err := ac.GetMixFormat(&wfx); err != nil {
		return fmt.Errorf("mix format: %w", err)
	}
	defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(wfx)))
	return fn(ac, wfx, deviceChanged)
}

const waveFormatExtensible = 0xFFFE
const waveFormatIEEEFloat = 3

func (w *wasapiCapturer) Run(ctx context.Context, onSamples func([]float32)) error {
	return withClient(func(ac *wca.IAudioClient, wfx *wca.WAVEFORMATEX, deviceChanged func() bool) error {
		if int(wfx.NSamplesPerSec) != w.rate {
			return fmt.Errorf("mix rate changed to %d Hz", wfx.NSamplesPerSec)
		}
		ch := int(wfx.NChannels)
		bits := int(wfx.WBitsPerSample)
		isFloat := wfx.WFormatTag == waveFormatIEEEFloat
		if wfx.WFormatTag == waveFormatExtensible && wfx.CbSize >= 22 {
			// WAVEFORMATEXTENSIBLE: SubFormat GUID's first uint32 is the format tag.
			ext := unsafe.Slice((*byte)(unsafe.Pointer(wfx)), 18+22)
			isFloat = binary.LittleEndian.Uint32(ext[24:28]) == waveFormatIEEEFloat
		}
		const bufferDuration = 200 * 10000 // 200 ms in 100 ns units
		if err := ac.Initialize(wca.AUDCLNT_SHAREMODE_SHARED, wca.AUDCLNT_STREAMFLAGS_LOOPBACK, bufferDuration, 0, wfx, nil); err != nil {
			return fmt.Errorf("initialize loopback: %w", err)
		}
		var acc *wca.IAudioCaptureClient
		if err := ac.GetService(wca.IID_IAudioCaptureClient, &acc); err != nil {
			return fmt.Errorf("capture client: %w", err)
		}
		defer acc.Release()
		if err := ac.Start(); err != nil {
			return fmt.Errorf("start: %w", err)
		}
		defer ac.Stop()

		inter := make([]float32, 0, 4096)
		mono := make([]float32, 0, 2048)
		// Loopback delivers no packets at all while nothing plays; feed
		// silence for that time so the bars fall and the level drops.
		zeros := make([]float32, w.rate/100)
		lastData := time.Now()
		lastCheck := time.Now()
		for ctx.Err() == nil {
			if time.Since(lastCheck) > 2*time.Second {
				lastCheck = time.Now()
				if deviceChanged() {
					return fmt.Errorf("default output device changed")
				}
			}
			var packet uint32
			if err := acc.GetNextPacketSize(&packet); err != nil {
				return fmt.Errorf("packet size: %w", err) // device invalidated, etc.
			}
			if packet == 0 {
				time.Sleep(10 * time.Millisecond)
				if time.Since(lastData) > 50*time.Millisecond {
					onSamples(zeros) // 10 ms of silence per idle 10 ms
				}
				continue
			}
			lastData = time.Now()
			for packet > 0 {
				var data *byte
				var frames, flags uint32
				var devPos, qpcPos uint64
				if err := acc.GetBuffer(&data, &frames, &flags, &devPos, &qpcPos); err != nil {
					return fmt.Errorf("get buffer: %w", err)
				}
				n := int(frames) * ch
				inter = inter[:0]
				if flags&wca.AUDCLNT_BUFFERFLAGS_SILENT != 0 || data == nil {
					for i := 0; i < n; i++ {
						inter = append(inter, 0)
					}
				} else {
					raw := unsafe.Slice(data, n*bits/8)
					switch {
					case isFloat && bits == 32:
						for i := 0; i < n; i++ {
							inter = append(inter, math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:])))
						}
					case bits == 16:
						for i := 0; i < n; i++ {
							inter = append(inter, float32(int16(binary.LittleEndian.Uint16(raw[i*2:])))/32768)
						}
					case bits == 32:
						for i := 0; i < n; i++ {
							inter = append(inter, float32(int32(binary.LittleEndian.Uint32(raw[i*4:])))/2147483648)
						}
					default:
						acc.ReleaseBuffer(frames)
						return fmt.Errorf("unsupported mix format: %d-bit", bits)
					}
				}
				if err := acc.ReleaseBuffer(frames); err != nil {
					return fmt.Errorf("release buffer: %w", err)
				}
				mono = toMonoF32(mono, inter, ch)
				onSamples(mono)
				if err := acc.GetNextPacketSize(&packet); err != nil {
					return fmt.Errorf("packet size: %w", err)
				}
			}
		}
		return nil
	})
}

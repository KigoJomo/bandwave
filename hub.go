package main

import (
	"context"
	"sync"
	"time"
)

// Source is anything that produces frames: this machine, or a remote agent.
type Source struct {
	Key        string // "local" or dial address
	Name       string
	OS         string
	Remote     bool
	Static     bool // given with --peer; keep retrying
	Connected  bool
	Frame      Frame
	Updated    time.Time
	LastActive time.Time
}

// Hub tracks every source the viewer knows about and which one it shows.
type Hub struct {
	mu       sync.Mutex
	sources  []*Source
	byKey    map[string]*Source
	dialing  map[string]bool
	selfName string
	hasLocal bool

	Auto    bool
	current int
	Errors  map[string]string // last dial error per address, for 'list -v'
}

func NewHub() *Hub {
	return &Hub{byKey: map[string]*Source{}, dialing: map[string]bool{}, Errors: map[string]string{}, selfName: hostname(), Auto: true}
}

func (h *Hub) add(s *Source) *Source {
	if old, ok := h.byKey[s.Key]; ok {
		return old
	}
	h.byKey[s.Key] = s
	h.sources = append(h.sources, s)
	return s
}

func (h *Hub) AddLocal(ctx context.Context, device string) {
	h.mu.Lock()
	s := h.add(&Source{Key: "local", Name: h.selfName + " (this pc)", Connected: true})
	h.hasLocal = true
	h.mu.Unlock()
	ls := StartLocal(ctx, device)
	go func() {
		for f := range ls.Frames() {
			h.push(s, f)
		}
	}()
}

func (h *Hub) push(s *Source, f Frame) {
	now := time.Now()
	h.mu.Lock()
	s.Frame, s.Updated = f, now
	if f.Level > silenceLevel {
		s.LastActive = now
	}
	h.mu.Unlock()
}

// Want asks the hub to connect to an agent at addr if it isn't already.
func (h *Hub) Want(ctx context.Context, addr, hint string, static bool) {
	h.mu.Lock()
	if h.dialing[addr] {
		h.mu.Unlock()
		return
	}
	if s, ok := h.byKey[addr]; ok && s.Connected {
		h.mu.Unlock()
		return
	}
	h.dialing[addr] = true
	h.mu.Unlock()
	go h.connect(ctx, addr, hint, static)
}

func (h *Hub) connect(ctx context.Context, addr, hint string, static bool) {
	defer func() {
		h.mu.Lock()
		delete(h.dialing, addr)
		h.mu.Unlock()
	}()
	backoff := time.Second
	for ctx.Err() == nil {
		var src *Source
		dctx, cancel := context.WithCancel(ctx)
		err := Dial(dctx, addr, func(hello Hello) {
			h.mu.Lock()
			defer h.mu.Unlock()
			// Our own agent duplicates the local source; another address for a
			// peer we already have (IPv4 vs IPv6 vs Tailscale) duplicates that.
			if h.hasLocal && hello.Name == h.selfName {
				cancel()
				return
			}
			for _, s := range h.sources {
				if s.Remote && s.Connected && s.Name == hello.Name && s.Key != addr {
					cancel()
					return
				}
			}
			src = h.add(&Source{Key: addr, Remote: true})
			src.Name, src.OS, src.Static, src.Connected = hello.Name, hello.OS, static, true
			backoff = time.Second
		}, func(f Frame) {
			if src != nil {
				h.push(src, f)
			}
		})
		cancel()
		h.mu.Lock()
		if err != nil && src == nil {
			h.Errors[addr] = err.Error()
		}
		h.mu.Unlock()
		if src != nil {
			h.mu.Lock()
			src.Connected = false
			h.mu.Unlock()
		}
		if !static && src == nil {
			return // discovery will ask again if it is still around
		}
		select {
		case <-ctx.Done():
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// Snapshot returns the source to draw plus a summary for the status line.
func (h *Hub) Snapshot() (cur Source, connected int, auto bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	live := func(s *Source) bool { return s.Connected && now.Sub(s.LastActive) < 1500*time.Millisecond }
	if h.Auto && len(h.sources) > 0 {
		if h.current >= len(h.sources) || !live(h.sources[h.current]) {
			best := -1
			for i, s := range h.sources {
				if live(s) && (best < 0 || s.LastActive.After(h.sources[best].LastActive)) {
					best = i
				}
			}
			if best >= 0 {
				h.current = best
			}
		}
	}
	for _, s := range h.sources {
		if s.Connected {
			connected++
		}
	}
	if h.current < len(h.sources) {
		cur = *h.sources[h.current]
		if !cur.Connected || now.Sub(cur.Updated) > time.Second {
			cur.Frame = Frame{}
		}
	}
	return cur, connected, h.Auto
}

// Step pins the view to the next/previous connected source.
func (h *Hub) Step(dir int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Auto = false
	n := len(h.sources)
	for i := 1; i <= n; i++ {
		j := ((h.current+dir*i)%n + n) % n
		if h.sources[j].Connected {
			h.current = j
			return
		}
	}
}

func (h *Hub) SetAuto() {
	h.mu.Lock()
	h.Auto = true
	h.mu.Unlock()
}

func (h *Hub) List() []Source {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Source, len(h.sources))
	for i, s := range h.sources {
		out[i] = *s
	}
	return out
}

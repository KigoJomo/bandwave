// Command bandwave is a cava-style terminal audio visualizer that can also
// show audio playing on other computers on your network.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
)

var version = "dev"

const usage = `bandwave %s - terminal audio visualizer for this PC and the PCs around it

Usage:
  bandwave [flags]            show the visualizer (default)
  bandwave serve [flags]      share this PC's audio spectrum with viewers
  bandwave list               list sources the viewer can find, then exit
  bandwave install            run 'serve' at login (systemd user unit / Windows logon task)
  bandwave uninstall          remove what 'install' set up
  bandwave version

Run 'bandwave <command> -h' for flags.

Keys: tab/n next source, p previous, a auto-follow, s status line, q quit.
`

func main() {
	if bi, ok := debug.ReadBuildInfo(); ok && version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = bi.Main.Version
	}
	log.SetFlags(0)
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "", "view":
		err = cmdView(args)
	case "serve", "agent":
		err = cmdServe(args)
	case "list":
		err = cmdList(args)
	case "install":
		err = install(args)
	case "uninstall":
		err = uninstall()
	case "version":
		fmt.Println("bandwave", version)
	case "help":
		fmt.Printf(usage, version)
	default:
		fmt.Fprintf(os.Stderr, usage, version)
		os.Exit(2)
	}
	if err != nil {
		log.Fatalf("bandwave: %v", err)
	}
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

type peerList []string

func (p *peerList) String() string { return strings.Join(*p, ",") }
func (p *peerList) Set(v string) error {
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			if _, _, err := net.SplitHostPort(s); err != nil {
				s = net.JoinHostPort(s, strconv.Itoa(DefaultPort))
			}
			*p = append(*p, s)
		}
	}
	return nil
}

type discoverOpts struct {
	peers       peerList
	noLAN, noKD bool
}

func (d *discoverOpts) flags(fs *flag.FlagSet) {
	fs.Var(&d.peers, "peer", "agent to show, host[:port]; repeatable or comma-separated")
	fs.BoolVar(&d.noLAN, "no-lan", false, "don't listen for LAN broadcast announcements")
	fs.BoolVar(&d.noKD, "no-kdeconnect", false, "don't look for agents on KDE Connect devices")
}

func (d *discoverOpts) start(ctx context.Context, hub *Hub) {
	for _, p := range d.peers {
		hub.Want(ctx, p, "", true)
	}
	if !d.noLAN {
		go listenBeacons(ctx, func(addr, name string) { hub.Want(ctx, addr, name, false) })
	}
	if !d.noKD {
		go func() {
			for ctx.Err() == nil {
				for addr, name := range kdeConnectPeers(ctx) {
					hub.Want(ctx, addr, name, false)
				}
				select {
				case <-ctx.Done():
				case <-time.After(20 * time.Second):
				}
			}
		}()
	}
}

func cmdView(args []string) error {
	fs := flag.NewFlagSet("bandwave", flag.ExitOnError)
	var d discoverOpts
	d.flags(fs)
	noLocal := fs.Bool("no-local", false, "don't capture this PC's audio")
	device := fs.String("device", "", "capture device (Linux: pulse source, default is the default output's monitor)")
	colors := fs.String("colors", "", "bar gradient bottom→top, e.g. '#5e62a9,#c45161' (default: your cava config, else terminal palette)")
	bw := fs.Int("bar-width", 2, "bar width in cells")
	gap := fs.Int("gap", 1, "space between bars")
	noStatus := fs.Bool("no-status", false, "hide the status line")
	only := fs.String("source", "", "start pinned to the source whose name contains this")
	fs.Usage = func() { fmt.Fprintf(fs.Output(), usage, version); fs.PrintDefaults() }
	fs.Parse(args)

	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("the viewer needs a terminal; use 'bandwave serve' to run headless")
	}
	st := Style{BarWidth: max(1, *bw), Gap: max(0, *gap), Status: !*noStatus}
	if *colors != "" {
		st.Colors = parseColors(*colors)
	} else {
		st.Colors = cavaColors()
	}

	ctx, cancel := signalContext()
	defer cancel()
	// Logs would scribble over the bars; keep them for after we exit.
	var logBuf strings.Builder
	log.SetOutput(&logBuf)
	defer func() {
		log.SetOutput(os.Stderr)
		if logBuf.Len() > 0 {
			io.WriteString(os.Stderr, logBuf.String())
		}
	}()

	hub := NewHub()
	if !*noLocal {
		hub.AddLocal(ctx, *device)
	}
	d.start(ctx, hub)

	restore, err := enterScreen()
	if err != nil {
		return err
	}
	defer restore()

	keys := make(chan byte, 16)
	go func() {
		b := make([]byte, 16)
		for {
			n, err := os.Stdin.Read(b)
			if err != nil {
				close(keys)
				return
			}
			for _, c := range b[:n] {
				keys <- c
			}
		}
	}()

	pinned := *only
	bars := NewBars()
	var sb strings.Builder
	var lastKey string
	t := time.NewTicker(time.Second / fps)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case c, ok := <-keys:
			if !ok {
				return nil
			}
			switch c {
			case 'q', 'Q', 3, 27:
				return nil
			case '\t', 'n', 'l':
				hub.Step(1)
			case 'p', 'h':
				hub.Step(-1)
			case 'a':
				hub.SetAuto()
			case 's':
				st.Status = !st.Status
			}
		case <-t.C:
			if pinned != "" {
				for i, s := range hub.List() {
					if s.Connected && strings.Contains(strings.ToLower(s.Name), strings.ToLower(pinned)) {
						hub.pin(i)
						pinned = ""
						break
					}
				}
			}
			w, h, err := term.GetSize(int(os.Stdout.Fd()))
			if err != nil || w < 4 || h < 2 {
				continue
			}
			cur, conn, auto := hub.Snapshot()
			if cur.Key != lastKey {
				bars = NewBars() // each source gets its own autosens
				lastKey = cur.Key
			}
			n := max(1, (w+st.Gap)/(st.BarWidth+st.Gap))
			vals := bars.Update(cur.Frame, n)
			mode := "auto"
			if !auto {
				mode = "pinned"
			}
			name := cur.Name
			if name == "" {
				name = "no source"
			}
			status := fmt.Sprintf(" %s · %s · %d source(s) · tab next · a auto · q quit", name, mode, conn)
			Draw(&sb, vals, w, h, st, status)
			io.WriteString(os.Stdout, sb.String())
		}
	}
}

func (h *Hub) pin(i int) {
	h.mu.Lock()
	h.Auto, h.current = false, i
	h.mu.Unlock()
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	listen := fs.String("listen", ":"+strconv.Itoa(DefaultPort), "address to listen on")
	device := fs.String("device", "", "capture device (Linux: pulse source)")
	noBeacon := fs.Bool("no-beacon", false, "don't announce on the LAN")
	logFile := fs.String("log", "", "append logs to this file instead of stderr")
	fs.Parse(args)
	if *logFile != "" {
		if f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			log.SetOutput(f)
			log.SetFlags(log.LstdFlags)
		}
	}
	ctx, cancel := signalContext()
	defer cancel()
	src := StartLocal(ctx, *device)
	return Serve(ctx, *listen, src, !*noBeacon)
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	var d discoverOpts
	d.flags(fs)
	wait := fs.Duration("wait", 4*time.Second, "how long to look")
	verbose := fs.Bool("v", false, "also show addresses that were tried and why they failed")
	fs.Parse(args)
	ctx, cancel := signalContext()
	defer cancel()
	hub := NewHub()
	d.start(ctx, hub)
	time.Sleep(*wait)
	found := 0
	for _, s := range hub.List() {
		if !s.Connected {
			continue
		}
		found++
		state := "silent"
		if time.Since(s.LastActive) < 2*time.Second {
			state = "playing"
		}
		fmt.Printf("%-24s %-8s %-22s %s\n", s.Name, s.OS, s.Key, state)
	}
	if *verbose {
		hub.mu.Lock()
		for addr, e := range hub.Errors {
			fmt.Printf("tried %-22s %s\n", addr, e)
		}
		hub.mu.Unlock()
	}
	if found == 0 {
		fmt.Println("no agents found. Run 'bandwave serve' (or 'bandwave install') on the other PCs.")
	}
	return nil
}

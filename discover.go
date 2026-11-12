package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type beaconMsg struct {
	Bandwave int    `json:"bandwave"`
	Name     string `json:"name"`
	Port     int    `json:"port"`
}

// runBeacon announces the agent on every IPv4 broadcast domain every 2 s.
func runBeacon(ctx context.Context, port int) {
	msg, _ := json.Marshal(beaconMsg{Bandwave: 1, Name: hostname(), Port: port})
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		log.Printf("beacon disabled: %v", err)
		return
	}
	defer conn.Close()
	setBroadcast(conn)
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		for _, b := range broadcastAddrs() {
			conn.WriteTo(msg, &net.UDPAddr{IP: b, Port: BeaconPort})
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func broadcastAddrs() []net.IP {
	out := []net.IP{net.IPv4bcast}
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagBroadcast == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil {
				continue
			}
			ip, mask := n.IP.To4(), n.Mask
			if len(mask) == 16 {
				mask = mask[12:]
			}
			b := make(net.IP, 4)
			for i := range b {
				b[i] = ip[i] | ^mask[i]
			}
			out = append(out, b)
		}
	}
	return out
}

// listenBeacons reports agents announcing themselves on the LAN.
func listenBeacons(ctx context.Context, found func(addr, name string)) {
	conn, err := net.ListenPacket("udp4", ":"+strconv.Itoa(BeaconPort))
	if err != nil {
		log.Printf("LAN discovery off (port %d busy?): %v", BeaconPort, err)
		return
	}
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	buf := make([]byte, 512)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		var m beaconMsg
		if json.Unmarshal(buf[:n], &m) != nil || m.Bandwave != 1 || m.Port <= 0 {
			continue
		}
		ua := from.(*net.UDPAddr)
		found(net.JoinHostPort(ua.IP.String(), strconv.Itoa(m.Port)), m.Name)
	}
}

var kdcAddr = regexp.MustCompile(`^- (.+): \S+ on (\S+) via LAN`)

// kdeConnectPeers lists the addresses of reachable KDE Connect devices. Any of
// them running a bandwave agent becomes a source, which also covers peers on
// other subnets or over Tailscale that LAN broadcasts can't reach.
func kdeConnectPeers(ctx context.Context) map[string]string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cli := kdeConnectCLI()
	if cli == "" {
		return nil
	}
	out, err := exec.CommandContext(ctx, cli, "-l").Output()
	if err != nil {
		return nil
	}
	peers := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		m := kdcAddr.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		peers[net.JoinHostPort(m[2], strconv.Itoa(DefaultPort))] = m[1]
	}
	return peers
}

// kdeConnectCLI finds kdeconnect-cli on PATH or where the Windows installers put it.
func kdeConnectCLI() string {
	if p, err := exec.LookPath("kdeconnect-cli"); err == nil {
		return p
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, base := range []string{os.Getenv("LOCALAPPDATA") + `\Programs`, os.Getenv("ProgramFiles")} {
		p := filepath.Join(base, "KDE Connect", "bin", "kdeconnect-cli.exe")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

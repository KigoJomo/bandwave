//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const unitName = "bandwave.service"

func unitPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "systemd", "user", unitName)
}

func install(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	extra := ""
	if len(args) > 0 {
		extra = " " + strings.Join(args, " ")
	}
	unit := fmt.Sprintf(`[Unit]
Description=bandwave agent: share this PC's audio spectrum with bandwave viewers
After=pipewire-pulse.service pulseaudio.service

[Service]
ExecStart=%s serve%s
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
`, exe, extra)
	p := unitPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(p, []byte(unit), 0o644); err != nil {
		return err
	}
	for _, a := range [][]string{{"daemon-reload"}, {"enable", "--now", unitName}, {"restart", unitName}} {
		if out, err := exec.Command("systemctl", append([]string{"--user"}, a...)...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl --user %s: %v\n%s", strings.Join(a, " "), err, out)
		}
	}
	fmt.Printf("installed %s and started it.\nIf a firewall is on, allow TCP %d and UDP %d from your LAN.\n", p, DefaultPort, BeaconPort)
	return nil
}

func uninstall() error {
	exec.Command("systemctl", "--user", "disable", "--now", unitName).Run()
	if err := os.Remove(unitPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	exec.Command("systemctl", "--user", "daemon-reload").Run()
	fmt.Println("removed", unitPath())
	return nil
}

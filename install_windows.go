//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const taskName = "bandwave agent"

// agentExe prefers bandwave-agent.exe (built without a console window) next
// to this binary, so the login task doesn't leave a window open.
func agentExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if a := filepath.Join(filepath.Dir(exe), "bandwave-agent.exe"); fileExists(a) {
		return a, nil
	}
	return exe, nil
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

func logPath() string {
	dir := os.Getenv("LOCALAPPDATA")
	if dir == "" {
		dir = os.TempDir()
	}
	dir = filepath.Join(dir, "bandwave")
	os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, "agent.log")
}

func install(args []string) error {
	exe, err := agentExe()
	if err != nil {
		return err
	}
	tr := fmt.Sprintf(`"%s" serve --log "%s"`, exe, logPath())
	if len(args) > 0 {
		tr += " " + strings.Join(args, " ")
	}
	// Runs as the current user at logon: WASAPI loopback needs the user's session.
	out, err := exec.Command("schtasks", "/Create", "/F", "/SC", "ONLOGON", "/RL", "LIMITED",
		"/TN", taskName, "/TR", tr).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks: %v\n%s", err, out)
	}
	exec.Command("schtasks", "/Run", "/TN", taskName).Run()
	fmt.Printf("installed logon task %q and started it (log: %s).\n", taskName, logPath())

	rule := []string{"advfirewall", "firewall", "add", "rule", "name=bandwave", "dir=in", "action=allow",
		"program=" + exe, "enable=yes", "profile=private,domain"}
	exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name=bandwave").Run()
	if out, err := exec.Command("netsh", rule...).CombinedOutput(); err != nil {
		fmt.Printf("\nCould not add the firewall rule (%s). From an admin prompt run:\n  netsh %s\n",
			strings.TrimSpace(string(out)), quoteArgs(rule))
	} else {
		fmt.Println("allowed bandwave through Windows Firewall on private/domain networks.")
	}
	return nil
}

func quoteArgs(a []string) string {
	q := make([]string, len(a))
	for i, s := range a {
		if strings.ContainsAny(s, " \t") {
			k, v, _ := strings.Cut(s, "=")
			s = k + `="` + v + `"`
		}
		q[i] = s
	}
	return strings.Join(q, " ")
}

func uninstall() error {
	exec.Command("schtasks", "/End", "/TN", taskName).Run()
	out, err := exec.Command("schtasks", "/Delete", "/F", "/TN", taskName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks: %v\n%s", err, out)
	}
	exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name=bandwave").Run()
	fmt.Println("removed the logon task (and the firewall rule, if this prompt was elevated).")
	return nil
}

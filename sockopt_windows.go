//go:build windows

package main

import (
	"net"

	"golang.org/x/sys/windows"
)

func setBroadcast(c net.PacketConn) {
	if uc, ok := c.(*net.UDPConn); ok {
		if raw, err := uc.SyscallConn(); err == nil {
			raw.Control(func(fd uintptr) {
				windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_BROADCAST, 1)
			})
		}
	}
}

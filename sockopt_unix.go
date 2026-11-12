//go:build !windows

package main

import (
	"net"
	"syscall"
)

func setBroadcast(c net.PacketConn) {
	if uc, ok := c.(*net.UDPConn); ok {
		if raw, err := uc.SyscallConn(); err == nil {
			raw.Control(func(fd uintptr) {
				syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
			})
		}
	}
}

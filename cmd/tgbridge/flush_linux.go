package main

import "syscall"

// flushTTY discards the terminal's unread input: ioctl(fd, TCFLSH, TCIFLUSH). Errors are ignored.
func flushTTY(fd uintptr) {
	const tcflsh, tciflush = 0x540B, 0 // shortcut: TCFLSH is 0x540B on amd64 and arm64 (the release targets), not on mips/ppc
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, fd, tcflsh, tciflush)
}

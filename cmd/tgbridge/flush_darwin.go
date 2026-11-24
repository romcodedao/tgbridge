package main

import (
	"syscall"
	"unsafe"
)

// flushTTY discards the terminal's unread input: ioctl(fd, TIOCFLUSH, &FREAD). Errors are ignored.
func flushTTY(fd uintptr) {
	const tiocflush = 0x80047410
	fread := int32(1)
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, fd, tiocflush, uintptr(unsafe.Pointer(&fread)))
}

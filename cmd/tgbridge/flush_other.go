//go:build !linux && !darwin

package main

// flushTTY is a no-op where tgbridge has no terminal flush.
func flushTTY(uintptr) {}

//go:build !windows

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func redirectStderrToFile(f *os.File) error {
	return unix.Dup2(int(f.Fd()), int(os.Stderr.Fd()))
}

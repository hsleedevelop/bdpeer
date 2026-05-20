//go:build windows

package main

import (
	"os"

	"golang.org/x/sys/windows"
)

func redirectStderrToFile(f *os.File) error {
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd())); err != nil {
		return err
	}
	os.Stderr = f
	return nil
}

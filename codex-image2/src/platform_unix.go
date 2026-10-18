//go:build !windows

package main

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

func replaceConfig(from, to string) error { return os.Rename(from, to) }
func detach(cmd *exec.Cmd)                { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
func openBrowser(address string) error {
	program := "xdg-open"
	if runtime.GOOS == "darwin" {
		program = "/usr/bin/open"
	}
	cmd := exec.Command(program, address)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

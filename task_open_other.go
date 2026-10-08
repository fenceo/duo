//go:build !windows

package main

import (
	"os/exec"
	"runtime"
)

func openTaskBrowser(address string) error {
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	return exec.Command(command, address).Run()
}

//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"time"
)

func startDetachedProcess(path string, args []string, directory string) (*os.Process, error) {
	command := exec.Command(path, args...)
	command.Dir = directory
	if err := command.Start(); err != nil {
		return nil, err
	}
	return command.Process, nil
}

func waitForProcessExit(pid int, timeout time.Duration) error {
	return errors.New("自动安装仅支持 Windows")
}

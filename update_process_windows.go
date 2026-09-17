//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func startDetachedProcess(path string, args []string, directory string) (*os.Process, error) {
	command := exec.Command(path, args...)
	command.Dir = directory
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	if err := command.Start(); err != nil {
		return nil, err
	}
	return command.Process, nil
}

func waitForProcessExit(pid int, timeout time.Duration) error {
	const processTerminate = 0x0001
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE|processTerminate, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		return err
	}
	defer windows.CloseHandle(handle)
	event, err := windows.WaitForSingleObject(handle, uint32(timeout/time.Millisecond))
	if err != nil {
		return err
	}
	if event == windows.WAIT_OBJECT_0 {
		return nil
	}
	if err = windows.TerminateProcess(handle, 0); err != nil {
		return fmt.Errorf("等待启动器退出超时，且无法结束旧启动器：%w", err)
	}
	event, err = windows.WaitForSingleObject(handle, 5000)
	if err != nil {
		return err
	}
	if event != windows.WAIT_OBJECT_0 {
		return errors.New("结束旧启动器超时")
	}
	return nil
}

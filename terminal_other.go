//go:build !windows

package main

import "errors"

func startTaskTerminal(Task) (taskPTY, error) {
	return nil, errors.New("此版本的交互终端需要在 Windows 上运行简作服务")
}

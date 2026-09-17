//go:build windows

package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"path/filepath"
)

func lockData(dir string) (func(), error) {
	name, e := windows.UTF16PtrFromString(filepath.Join(dir, "service.lock"))
	if e != nil {
		return nil, e
	}
	h, e := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		return nil, fmt.Errorf("数据目录正在使用或不可写：%w", e)
	}
	return func() { windows.CloseHandle(h) }, nil
}

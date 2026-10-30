//go:build windows

package main

import "golang.org/x/sys/windows"

func installVaultMigrationFile(from, to string) error {
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	// No MOVEFILE_REPLACE_EXISTING: an existing/concurrently created user file
	// must make migration fail. This also works on volumes without hard links.
	return windows.MoveFileEx(source, target, windows.MOVEFILE_WRITE_THROUGH)
}

//go:build !windows

package main

import "os"

func installVaultMigrationFile(from, to string) error {
	// Link is atomic and fails if the destination already exists. The caller
	// removes the staged name after installation.
	return os.Link(from, to)
}

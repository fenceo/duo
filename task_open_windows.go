//go:build windows

package main

import "os/exec"

func openTaskBrowser(address string) error {
	cmd := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", address)
	hideCommand(cmd)
	return cmd.Run()
}

//go:build !windows

package main

import "os/exec"

func hideCommand(cmd *exec.Cmd) {}

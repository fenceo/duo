package main

import (
	"context"
	"strings"
	"testing"
)

func TestEngineCheckUsesSelectedCredentialEnvironment(t *testing.T) {
	for _, target := range []string{"windows", "wsl", "ssh"} {
		t.Run(target, func(t *testing.T) {
			c := Config{EngineEnv: map[string]string{"CODEX_HOME": "/profile/selected"}}
			if target == "wsl" {
				c.Distro, c.User = "FixtureUbuntu", "fixture"
			} else if target == "ssh" {
				c.SSHHost = "fixture-host"
			}
			cmd := commandWithContext(context.Background(), engineCheckCommand(c, "codex", "login", "status"))
			args := strings.Join(cmd.Args, " ")
			env := strings.Join(cmd.Env, "\n")
			if target == "windows" {
				if !strings.Contains(env, "CODEX_HOME=/profile/selected") || strings.Contains(args, "CODEX_HOME=") {
					t.Fatal("native login check did not use the profile in the child environment")
				}
			} else if !strings.Contains(args, "CODEX_HOME=/profile/selected") || !strings.Contains(args, "env") {
				t.Fatal("remote login check omitted the selected profile")
			}
		})
	}
}

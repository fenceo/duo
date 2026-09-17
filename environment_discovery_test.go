package main

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestDecodeWSLDistros(t *testing.T) {
	text := "\ufeffUbuntu-22.04\r\ndocker-desktop\r\nUbuntu-22.04\n中文系统\n"
	var encoded []byte
	for _, u := range utf16.Encode([]rune(text)) {
		encoded = binary.LittleEndian.AppendUint16(encoded, u)
	}
	expected := []string{"Ubuntu-22.04", "中文系统"}
	if got := decodeWSLList(string(encoded)); !reflect.DeepEqual(got, expected) {
		t.Fatalf("UTF16: %q", got)
	}
	if got := decodeWSLList(text); !reflect.DeepEqual(got, expected) {
		t.Fatalf("UTF8: %q", got)
	}
}
func TestDetectedAuthConservative(t *testing.T) {
	for _, c := range []struct {
		path, out string
		err       error
		want      string
	}{
		{"", "", nil, "missing"},
		{"codex", "Logged in using API key: private-value", nil, "configured"},
		{"claude", "Auth method: claude.ai", nil, "configured"},
		{"codex", "Not logged in", nil, "login"},
		{"codex", "Logged in", errors.New("failure"), "unknown"},
		{"claude", "unrecognized output", nil, "unknown"},
		{"claude", "", context.DeadlineExceeded, "unknown"},
	} {
		got := detectedAuth(c.path, c.out, c.err)
		if got.State != c.want {
			t.Errorf("%q = %s, want %s", c.out, got.State, c.want)
		}
		if strings.Contains(got.Label, "private-value") {
			t.Fatal("raw auth output leaked")
		}
	}
}
func TestDiscoveryUsesDistroDefaultUserAndExactCLI(t *testing.T) {
	cmd := environmentProbeCommand(Environment{Type: "wsl", Distro: "Ubuntu-22.04", User: "dev"}, "sh", "-c", discoverWSLScript)
	if !reflect.DeepEqual(cmd.Args[1:], []string{"-d", "Ubuntu-22.04", "-u", "dev", "--exec", "sh", "-c", discoverWSLScript}) {
		t.Fatalf("WSL script would be expanded by an extra shell: %v", cmd.Args)
	}
	env := Environment{ID: "wsl", Type: "wsl", Distro: "Ubuntu-22.04", Workspaces: []string{"/home"}}
	probe := func(ctx context.Context, e Environment, args ...string) (string, error) {
		if e.Distro != "Ubuntu-22.04" {
			t.Error("wrong distro")
		}
		if args[0] == "sh" {
			if e.User != "" {
				t.Error("detection must use distro default user")
			}
			return "warning\n__JIANZUO_ENV__\ndev\n/home/dev\n/home/dev/.local/bin/codex\n/home/dev/.local/bin/claude\n", nil
		}
		if e.User != "dev" {
			t.Error("auth must use detected user")
		}
		if args[0] == "/home/dev/.local/bin/codex" && reflect.DeepEqual(args[1:], []string{"login", "status"}) {
			return "Not logged in", errors.New("not logged in")
		}
		if args[0] == "/home/dev/.local/bin/claude" && reflect.DeepEqual(args[1:], []string{"auth", "status", "--text"}) {
			return "Auth method: claude.ai", nil
		}
		t.Errorf("unexpected command (must not launch a task): %v", args)
		return "", errors.New("unexpected")
	}
	got := discoverOne(context.Background(), probe, env)
	if got.Environment.DefaultEngine != "claude" || got.Environment.User != "dev" || got.Environment.Workspaces[0] != "/home/dev" || got.Codex.State != "login" {
		t.Fatalf("bad detection: %+v", got)
	}
	if env.User != "" || env.Workspaces[0] != "/home" {
		t.Fatal("input config was mutated")
	}
	failed := discoverOne(context.Background(), func(context.Context, Environment, ...string) (string, error) { return "", context.DeadlineExceeded }, env)
	if failed.Message == "" || failed.Codex.State != "unknown" {
		t.Fatal("failed WSL detection must not claim installation/login")
	}
}

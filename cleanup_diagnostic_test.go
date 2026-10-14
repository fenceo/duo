package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func cleanupUTF16Fixture(text string) []byte {
	units := utf16.Encode([]rune(text))
	data := make([]byte, 2*len(units))
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[2*i:], unit)
	}
	return data
}

func TestCleanupDiagnosticClassesNeverExposeSource(t *testing.T) {
	for _, test := range []struct {
		name, text, want string
		ctxErr           error
		utf16            bool
	}{
		{name: "permission", text: "PermissionError: [Errno 1] Operation not permitted", want: "清理进程权限不足"},
		{name: "wsl-access", text: "Wsl/Service/E_ACCESSDENIED", want: "WSL 服务拒绝访问", utf16: true},
		{name: "wsl-service", text: "Wsl/Service/CreateInstance/HCS_E_SERVICE_NOT_AVAILABLE", want: "WSL 服务不可用", utf16: true},
		{name: "wsl-rpc", text: "Wsl/Service/0x800706ba", want: "WSL 服务不可用"},
		{name: "wsl-distro", text: "Wsl/Service/WSL_E_DISTRO_NOT_FOUND", want: "WSL 发行版不存在"},
		{name: "python-syntax", text: "IndentationError: unexpected indent", want: "Python 清理脚本解析失败"},
		{name: "python-missing", text: "sh: python3: command not found", want: "未找到 Python 3"},
		{name: "file-missing", text: "FileNotFoundError: /fixture/path", want: "清理所需文件或程序不存在"},
		{name: "unknown", text: "unclassified shell output", want: "未识别清理错误类别"},
		{name: "timeout", text: "PermissionError: fixture", ctxErr: context.DeadlineExceeded, want: "清理命令超时"},
		{name: "cancel", ctxErr: context.Canceled, want: "清理命令已取消"},
	} {
		t.Run(test.name, func(t *testing.T) {
			text := test.text + "\nfixture-private-token /fixture/private/path"
			data := []byte(text)
			if test.utf16 {
				data = cleanupUTF16Fixture(text)
			}
			if got := cleanupDiagnosticClass(test.ctxErr, data, nil); got != test.want {
				t.Fatalf("unexpected fixed diagnostic: %q", got)
			}
		})
	}
}

func TestCleanupDiagnosticBufferBoundsOutputWithoutStoppingWriter(t *testing.T) {
	var out cleanupDiagnosticBuffer
	large := []byte(strings.Repeat("fixture-private-token", cleanupDiagnosticLimit))
	if n, err := out.Write(large); n != len(large) || err != nil || len(out.data) != cleanupDiagnosticLimit {
		t.Fatal("large writes must drain while retaining only bounded output", n, err, len(out.data))
	}
	for range 32 {
		chunk := []byte(strings.Repeat("x", 1024))
		if n, err := out.Write(chunk); n != len(chunk) || err != nil || len(out.data) > cleanupDiagnosticLimit {
			t.Fatal("incremental output exceeded the bound or stopped draining", n, err, len(out.data))
		}
	}
	_, _ = out.Write([]byte("\nPermissionError: fixture-private-token"))
	if got := cleanupDiagnosticClass(nil, nil, out.data); got != "清理进程权限不足" {
		t.Fatal("lost final exception from long output", got)
	}
}

func TestCleanupDiagnosticProcessFixture(t *testing.T) {
	switch os.Getenv("DUO_TEST_CLEANUP_DIAGNOSTIC") {
	case "failure":
		fmt.Fprint(os.Stdout, strings.Repeat("fixture-private-token", cleanupDiagnosticLimit))
		fmt.Fprint(os.Stderr, strings.Repeat("fixture-private-token", cleanupDiagnosticLimit))
		fmt.Fprint(os.Stderr, "\nPermissionError: fixture-private-token")
		os.Exit(7)
	case "wsl":
		_, _ = os.Stdout.Write(cleanupUTF16Fixture("Wsl/Service/E_ACCESSDENIED fixture-private-token"))
		os.Exit(1)
	case "success":
		fmt.Fprint(os.Stderr, "PermissionError: fixture-private-token")
		os.Exit(0)
	case "held-pipe":
		exe, _ := os.Executable()
		child := exec.Command(exe, "-test.run=^TestCleanupDiagnosticProcessFixture$")
		child.Env = append(os.Environ(), "DUO_TEST_CLEANUP_DIAGNOSTIC=pipe-child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		hideCommand(child)
		if err := child.Start(); err != nil {
			os.Exit(8)
		}
		if err := os.WriteFile(os.Getenv("DUO_TEST_CLEANUP_PID_FILE"), []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
			_ = child.Process.Kill()
			_ = child.Wait()
			os.Exit(8)
		}
		os.Exit(0)
	case "pipe-child":
		time.Sleep(3 * time.Second)
		os.Exit(0)
	}
}

func TestCleanupDiagnosticCommandPreservesFailureAndPrimaryError(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		mode, want string
		exit       int
	}{
		{mode: "failure", want: "清理进程权限不足", exit: 7},
		{mode: "wsl", want: "WSL 服务拒绝访问", exit: 1},
		{mode: "success", exit: 0},
		{mode: "held-pipe", exit: 0},
	} {
		t.Run(test.mode, func(t *testing.T) {
			cmd := exec.Command(exe, "-test.run=^TestCleanupDiagnosticProcessFixture$")
			cmd.Env = append(os.Environ(), "DUO_TEST_CLEANUP_DIAGNOSTIC="+test.mode)
			if test.mode == "held-pipe" {
				pidFile := filepath.Join(t.TempDir(), "own-child.pid")
				cmd.Env = append(cmd.Env, "DUO_TEST_CLEANUP_PID_FILE="+pidFile)
				t.Cleanup(func() {
					// Let our short-lived fixture close the executable before Go
					// removes the test build. This never signals another process.
					data, err := os.ReadFile(pidFile)
					if err != nil {
						return
					}
					pid, err := strconv.Atoi(string(data))
					if err == nil {
						if child, err := os.FindProcess(pid); err == nil {
							_, _ = child.Wait()
						}
					}
				})
			}
			hideCommand(cmd)
			started := time.Now()
			err := runCodexCleanupCommand(context.Background(), cmd)
			if test.mode == "held-pipe" && time.Since(started) >= 2*time.Second {
				t.Fatal("capturing diagnostics waited for unrelated inherited output handles")
			}
			if test.exit == 0 {
				if err != nil {
					t.Fatal("diagnostic output changed successful cleanup into failure", err)
				}
				return
			}
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != test.exit {
				t.Fatal("original cleanup exit status lost", err)
			}
			if want := fmt.Sprintf("exit status %d；清理诊断：%s", test.exit, test.want); err.Error() != want {
				t.Fatalf("unexpected or unredacted diagnostic: %q", err)
			}
			primary := errors.New("synthetic stream disconnected before completion")
			combined := codexCleanupError(primary, err, false)
			if !errors.Is(combined, primary) || !strings.HasPrefix(combined.Error(), primary.Error()) || strings.Contains(combined.Error(), "fixture-private-token") {
				t.Fatal("cleanup masked primary failure or exposed source output", combined)
			}
		})
	}
}

package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const cleanupDiagnosticLimit = 16 * 1024

// Keep the tail containing Python's final exception while continuing to drain
// output. Returning a short write would alter the cleanup process's behavior.
type cleanupDiagnosticBuffer struct{ data []byte }

func (b *cleanupDiagnosticBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n >= cleanupDiagnosticLimit {
		b.data = append(b.data[:0], p[n-cleanupDiagnosticLimit:]...)
		return n, nil
	}
	if excess := len(b.data) + n - cleanupDiagnosticLimit; excess > 0 {
		copy(b.data, b.data[excess:])
		b.data = b.data[:len(b.data)-excess]
	}
	b.data = append(b.data, p...)
	return n, nil
}

// Only fixed, allowlisted labels leave this helper. Cleanup output can contain
// shell profile contents, paths or credentials and must never be echoed back.
func cleanupDiagnosticClass(ctxErr error, stdout, stderr []byte) string {
	if ctxErr == context.DeadlineExceeded {
		return "清理命令超时"
	}
	if ctxErr == context.Canceled {
		return "清理命令已取消"
	}
	output := strings.ToLower(string(stdout) + "\n" + string(stderr) + "\n" + decodeWSLText(stdout) + "\n" + decodeWSLText(stderr))
	containsAny := func(values ...string) bool {
		for _, value := range values {
			if strings.Contains(output, value) {
				return true
			}
		}
		return false
	}
	if containsAny("wsl/", "service/") {
		switch {
		case containsAny("e_accessdenied"):
			return "WSL 服务拒绝访问"
		case containsAny("hcs_e_service_not_available", "rpc_s_server_unavailable", "wsl_e_service_unavailable", "0x800706ba", "error_service_not_active", "error_service_disabled"):
			return "WSL 服务不可用"
		case containsAny("wsl_e_distro_not_found"):
			return "WSL 发行版不存在"
		}
	}
	switch {
	case containsAny("permissionerror:", "permission denied", "operation not permitted"):
		return "清理进程权限不足"
	case containsAny("syntaxerror:", "indentationerror:", "taberror:"):
		return "Python 清理脚本解析失败"
	case containsAny("python3: not found", "python3: command not found"):
		return "未找到 Python 3"
	case containsAny("filenotfounderror:"):
		return "清理所需文件或程序不存在"
	default:
		return "未识别清理错误类别"
	}
}

func runCodexCleanupCommand(ctx context.Context, cmd *exec.Cmd) error {
	var stdout, stderr cleanupDiagnosticBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// Capturing output adds pipes which a shell's child might keep open. Bound
	// draining them without changing a successful cleanup command's exit status.
	if cmd.WaitDelay == 0 {
		cmd.WaitDelay = 250 * time.Millisecond
	}
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
			return nil
		}
		return fmt.Errorf("%w；清理诊断：%s", err, cleanupDiagnosticClass(ctx.Err(), stdout.data, stderr.data))
	}
	return nil
}

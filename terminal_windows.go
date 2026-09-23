//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsPTY struct {
	in, out      *os.File
	console, job windows.Handle
	mu           sync.Mutex
	once         sync.Once
	stopping     chan struct{}
	output       chan []byte
	exit         chan uint32
}

func startTaskTerminal(t Task) (taskPTY, error) {
	args, dir, err := terminalArgs(t)
	if err != nil {
		return nil, err
	}
	base := filepath.Join(os.Getenv("SystemRoot"), "System32")
	switch args[0] {
	case "powershell.exe":
		args[0] = filepath.Join(base, "WindowsPowerShell", "v1.0", "powershell.exe")
	case "wsl.exe":
		args[0] = filepath.Join(base, "wsl.exe")
	case "ssh.exe":
		args[0] = filepath.Join(base, "OpenSSH", "ssh.exe")
	}
	return startWindowsPTY(args, dir)
}

func startWindowsPTY(args []string, dir string) (*windowsPTY, error) {
	if err := windows.NewLazySystemDLL("kernel32.dll").NewProc("CreatePseudoConsole").Find(); err != nil {
		return nil, fmt.Errorf("交互终端需要 Windows 10 1809 或更新版本：%w", err)
	}
	// A service/test host can inherit "ignore Ctrl+C" from its launcher. That
	// flag is inherited by children, unlike Go's registered control handler.
	// Clear only that flag so the new shell can receive terminal interrupts.
	windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleCtrlHandler").Call(0, 0)
	app, err := windows.UTF16PtrFromString(args[0])
	if err != nil {
		return nil, err
	}
	cmd, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(args))
	if err != nil {
		return nil, err
	}
	var cwd *uint16
	if dir != "" {
		cwd, err = windows.UTF16PtrFromString(dir)
		if err != nil {
			return nil, err
		}
	}
	var inRead, inWrite, outRead, outWrite windows.Handle
	if err = windows.CreatePipe(&inRead, &inWrite, nil, 0); err != nil {
		return nil, err
	}
	defer windows.CloseHandle(inRead)
	if err = windows.CreatePipe(&outRead, &outWrite, nil, 0); err != nil {
		windows.CloseHandle(inWrite)
		return nil, err
	}
	defer windows.CloseHandle(outWrite)
	p := &windowsPTY{in: os.NewFile(uintptr(inWrite), "terminal-input"), out: os.NewFile(uintptr(outRead), "terminal-output"), stopping: make(chan struct{}), output: make(chan []byte, 32), exit: make(chan uint32, 1)}
	if err = windows.CreatePseudoConsole(windows.Coord{X: 100, Y: 30}, inRead, outWrite, 0, &p.console); err != nil {
		p.in.Close()
		p.out.Close()
		return nil, fmt.Errorf("需要 Windows 10 1809 或更新版本：%w", err)
	}
	// Keep draining throughout ClosePseudoConsole: it can emit a final frame.
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buffer := make([]byte, 16384)
		for {
			n, e := p.out.Read(buffer)
			if n > 0 {
				chunk := append([]byte(nil), buffer[:n]...)
				select {
				case p.output <- chunk:
				case <-p.stopping:
					// Preserve a final frame when there is room, but never let a
					// disconnected consumer block pseudoconsole teardown.
					select {
					case p.output <- chunk:
					default:
					}
				}
			}
			if e != nil {
				return
			}
		}
	}()
	failure := func(e error) (*windowsPTY, error) { p.Close(); return nil, e }
	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return failure(err)
	}
	defer attrs.Delete()
	if err = setPseudoConsoleAttribute(attrs, p.console); err != nil {
		return failure(err)
	}
	// Zero standard handles prevent inheriting the service's redirected pipes.
	info := windows.StartupInfoEx{StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{})), Flags: windows.STARTF_USESTDHANDLES}, ProcThreadAttributeList: attrs.List()}
	p.job, err = windows.CreateJobObject(nil, nil)
	if err != nil {
		return failure(err)
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(p.job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return failure(err)
	}
	var process windows.ProcessInformation
	var envBlock *uint16
	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT | windows.CREATE_SUSPENDED)
	if strings.EqualFold(filepath.Base(args[0]), "powershell.exe") {
		// Let Windows PowerShell initialize its own module path instead of
		// inheriting a PowerShell 7 host's incompatible PSReadLine module.
		env := []string{}
		for _, item := range os.Environ() {
			if !strings.HasPrefix(strings.ToLower(item), "psmodulepath=") {
				env = append(env, item)
			}
		}
		block := utf16.Encode([]rune(strings.Join(env, "\x00") + "\x00\x00"))
		envBlock = &block[0]
		flags |= windows.CREATE_UNICODE_ENVIRONMENT
	} else if strings.EqualFold(filepath.Base(args[0]), "wsl.exe") {
		// Keep Windows shims and restricted-process variables out of the Linux
		// shell. The selected WSL user's own login environment remains intact.
		block := utf16.Encode([]rune(strings.Join(wslHostEnvironment(), "\x00") + "\x00\x00"))
		envBlock = &block[0]
		flags |= windows.CREATE_UNICODE_ENVIRONMENT
	}
	if err = windows.CreateProcess(app, cmd, nil, nil, false, flags, envBlock, cwd, &info.StartupInfo, &process); err != nil {
		return failure(err)
	}
	defer windows.CloseHandle(process.Thread)
	if err = windows.AssignProcessToJobObject(p.job, process.Process); err == nil {
		_, err = windows.ResumeThread(process.Thread)
	}
	if err != nil {
		windows.TerminateProcess(process.Process, 1)
		windows.CloseHandle(process.Process)
		return failure(err)
	}
	go func() {
		windows.WaitForSingleObject(process.Process, windows.INFINITE)
		var code uint32
		windows.GetExitCodeProcess(process.Process, &code)
		windows.CloseHandle(process.Process)
		p.Close()
		<-readerDone
		p.exit <- code
		close(p.output)
	}()
	return p, nil
}

// HPCON is an opaque Windows handle passed by value, unlike attributes that
// point at Go data. Do not convert it to an unsafe.Pointer and retain it in the
// x/sys attribute container's GC-visible pointers slice.
// https://learn.microsoft.com/windows/console/creating-a-pseudoconsole-session
func setPseudoConsoleAttribute(attrs *windows.ProcThreadAttributeListContainer, console windows.Handle) error {
	result, _, err := windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute").Call(
		uintptr(unsafe.Pointer(attrs.List())), 0, windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		uintptr(console), unsafe.Sizeof(console), 0, 0,
	)
	runtime.KeepAlive(attrs)
	if result == 0 {
		if err == windows.ERROR_SUCCESS {
			err = windows.ERROR_GEN_FAILURE
		}
		return fmt.Errorf("UpdateProcThreadAttribute: %w", err)
	}
	return nil
}

func (p *windowsPTY) Write(b []byte) (int, error) { return p.in.Write(b) }
func (p *windowsPTY) Output() <-chan []byte       { return p.output }
func (p *windowsPTY) Exit() <-chan uint32         { return p.exit }
func (p *windowsPTY) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.console == 0 {
		return os.ErrClosed
	}
	return windows.ResizePseudoConsole(p.console, windows.Coord{X: int16(cols), Y: int16(rows)})
}
func (p *windowsPTY) Close() {
	p.once.Do(func() {
		close(p.stopping)
		if p.job != 0 {
			windows.TerminateJobObject(p.job, 1)
		}
		p.in.Close()
		p.mu.Lock()
		if p.console != 0 {
			windows.ClosePseudoConsole(p.console)
			p.console = 0
		}
		p.mu.Unlock()
		p.out.Close()
		if p.job != 0 {
			windows.CloseHandle(p.job)
		}
	})
}

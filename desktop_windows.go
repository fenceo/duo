//go:build windows && amd64

package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var desktopUser = windows.NewLazySystemDLL("user32.dll")
var desktopGDI = windows.NewLazySystemDLL("gdi32.dll")

func desktopDPI() func() {
	runtime.LockOSThread()
	set := desktopUser.NewProc("SetThreadDpiAwarenessContext")
	old, _, _ := set.Call(^uintptr(3))
	return func() {
		if old != 0 {
			set.Call(old)
		}
		runtime.UnlockOSThread()
	}
}
func desktopForeground() uintptr {
	v, _, _ := desktopUser.NewProc("GetForegroundWindow").Call()
	return v
}
func desktopLastInput() (uint32, error) {
	info := struct{ Size, Tick uint32 }{Size: 8}
	ok, _, _ := desktopUser.NewProc("GetLastInputInfo").Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return 0, errors.New("无法检查用户输入状态")
	}
	return info.Tick, nil
}
func desktopVirtualBounds() DesktopBounds {
	metric := desktopUser.NewProc("GetSystemMetrics")
	get := func(n uintptr) int { v, _, _ := metric.Call(n); return int(int32(v)) }
	return DesktopBounds{X: get(76), Y: get(77), Width: get(78), Height: get(79)}
}
func desktopWindowPID(hwnd uintptr) uint32 {
	var pid uint32
	desktopUser.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid
}
func nativeDesktopTargets() ([]DesktopTarget, error) {
	restore := desktopDPI()
	defer restore()
	if desktopForeground() == 0 {
		return nil, errors.New("当前没有可共享的交互桌面，请解锁服务电脑")
	}
	targets := []DesktopTarget{{ID: "desktop", Title: "整个桌面（所有显示器）"}}
	callback := syscall.NewCallback(func(hwnd, unused uintptr) uintptr {
		visible, _, _ := desktopUser.NewProc("IsWindowVisible").Call(hwnd)
		if visible == 0 {
			return 1
		}
		minimized, _, _ := desktopUser.NewProc("IsIconic").Call(hwnd)
		if minimized != 0 {
			return 1
		}
		var title [1024]uint16
		n, _, _ := desktopUser.NewProc("GetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(&title[0])), 1024)
		if n > 0 && len(targets) < 100 {
			targets = append(targets, DesktopTarget{ID: fmt.Sprintf("%x:%d", hwnd, desktopWindowPID(hwnd)), Title: windows.UTF16ToString(title[:])})
		}
		return 1
	})
	ok, _, _ := desktopUser.NewProc("EnumWindows").Call(callback, 0)
	if ok == 0 {
		return nil, errors.New("读取窗口列表失败")
	}
	return targets, nil
}
func desktopTargetBounds(target string) (DesktopBounds, error) {
	screen := desktopVirtualBounds()
	if screen.Width <= 0 || screen.Height <= 0 {
		return DesktopBounds{}, errors.New("无法读取桌面尺寸")
	}
	if target == "desktop" {
		return screen, nil
	}
	parts := strings.Split(target, ":")
	if len(parts) != 2 {
		return DesktopBounds{}, errors.New("共享窗口标识无效")
	}
	handle, err := strconv.ParseUint(parts[0], 16, 64)
	pid, pidErr := strconv.ParseUint(parts[1], 10, 32)
	if err != nil || pidErr != nil || handle == 0 || desktopWindowPID(uintptr(handle)) != uint32(pid) {
		return DesktopBounds{}, errors.New("共享窗口已关闭，请重新选择")
	}
	if desktopForeground() != uintptr(handle) {
		return DesktopBounds{}, errors.New("请先将共享窗口切到前台，再重新观察")
	}
	var rect struct{ Left, Top, Right, Bottom int32 }
	ok, _, _ := desktopUser.NewProc("GetWindowRect").Call(uintptr(handle), uintptr(unsafe.Pointer(&rect)))
	if ok == 0 {
		return DesktopBounds{}, errors.New("无法读取共享窗口位置")
	}
	x, y := max(int(rect.Left), screen.X), max(int(rect.Top), screen.Y)
	right, bottom := min(int(rect.Right), screen.X+screen.Width), min(int(rect.Bottom), screen.Y+screen.Height)
	if right <= x || bottom <= y {
		return DesktopBounds{}, errors.New("共享窗口不在可见桌面中")
	}
	return DesktopBounds{X: x, Y: y, Width: right - x, Height: bottom - y}, nil
}
func nativeDesktopCapture(target string) (desktopObservation, error) {
	return nativeDesktopCaptureAs(target, false)
}
func nativeDesktopCaptureHuman(target string) (desktopObservation, error) {
	return nativeDesktopCaptureAs(target, true)
}
func nativeDesktopCaptureAs(target string, human bool) (desktopObservation, error) {
	restore := desktopDPI()
	defer restore()
	out := desktopObservation{Target: target}
	var err error
	out.Foreground = desktopForeground()
	if out.Foreground == 0 {
		return out, errors.New("桌面已锁定或不可用")
	}
	out.Input, err = desktopLastInput()
	if err != nil {
		return out, err
	}
	out.Bounds, err = desktopTargetBounds(target)
	if err != nil {
		return out, err
	}
	b := out.Bounds
	if int64(b.Width)*int64(b.Height) > 32*1024*1024 {
		return out, errors.New("共享画面过大，请选择一个窗口")
	}
	dc, _, _ := desktopUser.NewProc("GetDC").Call(0)
	if dc == 0 {
		return out, errors.New("无法打开桌面画面")
	}
	defer desktopUser.NewProc("ReleaseDC").Call(0, dc)
	mem, _, _ := desktopGDI.NewProc("CreateCompatibleDC").Call(dc)
	if mem == 0 {
		return out, errors.New("无法创建截图上下文")
	}
	defer desktopGDI.NewProc("DeleteDC").Call(mem)
	header := struct {
		Size                   uint32
		Width, Height          int32
		Planes, BitCount       uint16
		Compression, SizeImage uint32
		XPels, YPels           int32
		Used, Important        uint32
	}{Size: 40, Width: int32(b.Width), Height: -int32(b.Height), Planes: 1, BitCount: 32}
	var pixels unsafe.Pointer
	bitmap, _, _ := desktopGDI.NewProc("CreateDIBSection").Call(dc, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&pixels)), 0, 0)
	if bitmap == 0 || pixels == nil {
		return out, errors.New("无法创建截图位图")
	}
	defer desktopGDI.NewProc("DeleteObject").Call(bitmap)
	old, _, _ := desktopGDI.NewProc("SelectObject").Call(mem, bitmap)
	defer desktopGDI.NewProc("SelectObject").Call(mem, old)
	ok, _, _ := desktopGDI.NewProc("BitBlt").Call(mem, 0, 0, uintptr(b.Width), uintptr(b.Height), dc, uintptr(b.X), uintptr(b.Y), 0x00CC0020|0x40000000)
	if ok == 0 {
		return out, errors.New("截图失败，桌面可能已锁定")
	}
	desktopGDI.NewProc("GdiFlush").Call()
	raw := unsafe.Slice((*byte)(pixels), b.Width*b.Height*4)
	img := image.NewRGBA(image.Rect(0, 0, b.Width, b.Height))
	for i := 0; i < len(raw); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = raw[i+2], raw[i+1], raw[i], 255
	}
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, img); err != nil {
		return out, errors.New("截图编码失败")
	}
	after, err := desktopLastInput()
	if err != nil || !human && after != out.Input || desktopForeground() != out.Foreground {
		return out, errors.New("用户正在操作桌面，请稍后重新观察")
	}
	afterBounds, err := desktopTargetBounds(target)
	if err != nil || afterBounds != out.Bounds {
		return out, errors.New("共享区域已变化，请重新观察")
	}
	out.Data = encoded.Bytes()
	return out, nil
}

type desktopInput [40]byte

func keyboardInput(code, scan uint16, flags uint32) desktopInput {
	var v desktopInput
	binary.LittleEndian.PutUint32(v[0:4], 1)
	binary.LittleEndian.PutUint16(v[8:10], code)
	binary.LittleEndian.PutUint16(v[10:12], scan)
	binary.LittleEndian.PutUint32(v[12:16], flags)
	return v
}
func mouseInput(x, y int32, data, flags uint32) desktopInput {
	var v desktopInput
	binary.LittleEndian.PutUint32(v[8:12], uint32(x))
	binary.LittleEndian.PutUint32(v[12:16], uint32(y))
	binary.LittleEndian.PutUint32(v[16:20], data)
	binary.LittleEndian.PutUint32(v[20:24], flags)
	return v
}
func nativeDesktopInput(observation desktopObservation, action DesktopAction) error {
	restore := desktopDPI()
	defer restore()
	if desktopForeground() != observation.Foreground {
		return errors.New("前台窗口已变化，请重新观察")
	}
	bounds, err := desktopTargetBounds(observation.Target)
	if err != nil {
		return err
	}
	if bounds != observation.Bounds {
		return errors.New("共享区域已移动或缩放，请重新观察")
	}
	tick, err := desktopLastInput()
	if err != nil {
		return err
	}
	if !observation.Human && tick != observation.Input {
		return errors.New("检测到用户接管，请等待用户完成操作并重新观察")
	}
	entries := []desktopInput{}
	switch action.Action {
	case "click", "double_click", "right_click", "scroll":
		screen := desktopVirtualBounds()
		x := int32((int64(bounds.X+action.X-screen.X) * 65535) / int64(max(1, screen.Width-1)))
		y := int32((int64(bounds.Y+action.Y-screen.Y) * 65535) / int64(max(1, screen.Height-1)))
		entries = append(entries, mouseInput(x, y, 0, 0x8000|0x4000|1))
		if action.Action == "scroll" {
			entries = append(entries, mouseInput(0, 0, uint32(int32(action.Delta*120)), 0x800))
		} else {
			down, up := uint32(2), uint32(4)
			if action.Action == "right_click" {
				down, up = 8, 16
			}
			entries = append(entries, mouseInput(0, 0, 0, down), mouseInput(0, 0, 0, up))
			if action.Action == "double_click" {
				entries = append(entries, mouseInput(0, 0, 0, down), mouseInput(0, 0, 0, up))
			}
		}
	case "type":
		for _, code := range utf16.Encode([]rune(action.Text)) {
			entries = append(entries, keyboardInput(0, code, 4), keyboardInput(0, code, 6))
		}
	case "key":
		keys, err := desktopKeyCodes(action.Key)
		if err != nil {
			return err
		}
		for _, key := range keys {
			entries = append(entries, keyboardInput(key, 0, 0))
		}
		for i := len(keys) - 1; i >= 0; i-- {
			entries = append(entries, keyboardInput(keys[i], 0, 2))
		}
	default:
		return errors.New("不支持的桌面动作")
	}
	if len(entries) == 0 {
		return errors.New("没有可发送的输入")
	}
	count, _, _ := desktopUser.NewProc("SendInput").Call(uintptr(len(entries)), uintptr(unsafe.Pointer(&entries[0])), 40)
	if int(count) != len(entries) {
		return errors.New("Windows 未完整接收输入；请检查画面，不要重复发送。管理员窗口和安全桌面可能拒绝控制")
	}
	return nil
}

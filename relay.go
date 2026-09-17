package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// A0/address/opcode/sum(low byte), per the user's CH340 relay manual.
type RelayConfig struct {
	Channel      int    `json:"channel"`
	Contact      string `json:"contact"`
	CycleSeconds int    `json:"cycle_seconds"`
}

func validateRelay(c HardwareConfig) error {
	if c.Kind == "" || c.Kind == "console" {
		if c.Relay != nil {
			return errors.New("普通设备不使用继电器参数")
		}
		return nil
	}
	if c.Kind != "relay" {
		return errors.New("设备类型无效")
	}
	if c.Relay == nil || c.Relay.Channel < 1 || c.Relay.Channel > 254 {
		return errors.New("继电器地址须为 1–254")
	}
	if c.Relay.Contact != "no" && c.Relay.Contact != "nc" {
		return errors.New("请选择实际接线：COM+NO 或 COM+NC")
	}
	if c.Relay.CycleSeconds < 1 || c.Relay.CycleSeconds > 60 {
		return errors.New("断电间隔须为 1–60 秒")
	}
	if c.Protocol != "serial" && c.Protocol != "tcp" {
		return errors.New("电源控制器使用串口或原始 TCP 串口网关")
	}
	if c.Protocol == "serial" && (c.Baud != 9600 || (c.DataBits != 0 && c.DataBits != 8) || (c.Parity != "" && c.Parity != "none") || (c.StopBits != "" && c.StopBits != "1")) {
		return errors.New("此继电器协议使用 9600 / 8N1")
	}
	return nil
}
func relayFrame(channel int, op byte) []byte {
	return []byte{0xa0, byte(channel), op, byte((0xa0 + channel + int(op)) & 255)}
}

type relaySession struct {
	mu                sync.Mutex
	config            RelayConfig
	channel           byte
	buffer            []byte
	known, coil, busy bool
	stamp, version    int64
	changed           chan struct{}
}

func newRelaySession(config RelayConfig) *relaySession {
	return &relaySession{config: config, channel: byte(config.Channel), changed: make(chan struct{})}
}
func (s *relaySession) isBusy() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.busy }
func (s *relaySession) receive(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buffer = append(s.buffer, b...)
	for len(s.buffer) >= 4 {
		f := s.buffer[:4]
		if f[0] != 0xa0 || f[1] != s.channel || f[2] > 1 || byte(uint16(f[0])+uint16(f[1])+uint16(f[2])) != f[3] {
			s.buffer = s.buffer[1:]
			continue
		}
		s.known = true
		s.coil = f[2] == 1
		s.stamp = now()
		s.version++
		close(s.changed)
		s.changed = make(chan struct{})
		s.buffer = s.buffer[4:]
	}
}
func (s *relaySession) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	power := s.coil
	if s.config.Contact == "nc" {
		power = !power
	}
	return map[string]any{"known": s.known, "coil": s.coil, "power": power, "updated": s.stamp, "busy": s.busy}
}
func (h *Hardware) relayCommand(ctx context.Context, c HardwareConfig, l *hardwareLink, task, generation string, op byte, expected *bool) error {
	l.relay.mu.Lock()
	version := l.relay.version
	l.relay.known = false
	l.relay.mu.Unlock()
	if err := h.sendOwned(c.ID, generation, task, relayFrame(c.Relay.Channel, op)); err != nil {
		return err
	}
	timer := time.NewTimer(1500 * time.Millisecond)
	defer timer.Stop()
	for {
		l.relay.mu.Lock()
		fresh := l.relay.version > version
		coil := l.relay.coil
		changed := l.relay.changed
		l.relay.mu.Unlock()
		if fresh {
			if expected != nil && coil != *expected {
				return errors.New("收到反馈，但继电器状态与指令不符")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("指令已发送，但未收到有效反馈；状态未知，未自动重试")
		case <-changed:
		}
	}
}
func (h *Hardware) operateRelay(ctx context.Context, c HardwareConfig, task, generation, action string) error {
	if err := validateRelay(c); err != nil {
		return err
	}
	if c.Kind != "relay" {
		return errors.New("此设备不是电源控制器")
	}
	if action != "on" && action != "off" && action != "query" && action != "cycle" {
		return errors.New("电源操作无效")
	}
	h.mu.Lock()
	l := h.links[c.ID]
	if l == nil || generation == "" || generation != l.generation || l.controller != task {
		h.mu.Unlock()
		return errors.New("请连接设备并取得控制权")
	}
	if l.readonly || l.relay == nil {
		h.mu.Unlock()
		return errors.New("当前连接不允许电源操作")
	}
	l.relay.mu.Lock()
	if l.relay.busy {
		l.relay.mu.Unlock()
		h.mu.Unlock()
		return errors.New("已有电源操作进行中，请等待")
	}
	l.relay.busy = true
	l.relay.mu.Unlock()
	h.mu.Unlock()
	// Use the settings of this connection, even if the request began before
	// the device was edited and reconnected.
	config := l.relay.config
	c.Relay = &config
	defer func() { l.relay.mu.Lock(); l.relay.busy = false; l.relay.mu.Unlock() }()
	setPower := func(on bool) error {
		coil := on
		if c.Relay.Contact == "nc" {
			coil = !coil
		}
		op := byte(2)
		if coil {
			op = 3
		}
		return h.relayCommand(ctx, c, l, task, generation, op, &coil)
	}
	h.recordFor(c.ID, "status", []byte("电源操作开始："+action), task)
	var err error
	switch action {
	case "query":
		err = h.relayCommand(ctx, c, l, task, generation, 5, nil)
	case "on":
		err = setPower(true)
	case "off":
		err = setPower(false)
	case "cycle":
		if err = setPower(false); err == nil {
			timer := time.NewTimer(time.Duration(c.Relay.CycleSeconds) * time.Second)
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-timer.C:
				err = setPower(true)
			}
			timer.Stop()
		}
	}
	result := "收到继电器反馈：" + action
	if err != nil {
		result = fmt.Sprintf("电源操作 %s 未确认完成：%v", action, err)
	}
	h.recordFor(c.ID, "status", []byte(result), task)
	return err
}
func (s *Server) relayAction(w http.ResponseWriter, r *http.Request) {
	c, err := s.hardwareConfig(r)
	if err != nil {
		fail(w, 404, "设备不存在")
		return
	}
	var v struct {
		Action       string `json:"action"`
		ConnectionID string `json:"connection_id"`
		Confirmed    bool   `json:"confirmed"`
	}
	if !body(w, r, &v) {
		return
	}
	if v.Action != "query" && !v.Confirmed {
		fail(w, 400, "请确认目标设备和电源操作")
		return
	}
	err = s.app.hardware.operateRelay(r.Context(), c, r.PathValue("id"), v.ConnectionID, v.Action)
	if err != nil {
		fail(w, 409, err.Error())
		return
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
}

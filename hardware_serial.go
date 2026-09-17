package main

import (
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

func serialMode(c HardwareConfig) (*serial.Mode, error) {
	if c.DataBits == 0 {
		c.DataBits = 8
	}
	if c.Parity == "" {
		c.Parity = "none"
	}
	if c.StopBits == "" {
		c.StopBits = "1"
	}
	if c.DataBits < 5 || c.DataBits > 8 {
		return nil, errors.New("串口数据位须为 5–8")
	}
	parities := map[string]serial.Parity{"none": serial.NoParity, "odd": serial.OddParity, "even": serial.EvenParity, "mark": serial.MarkParity, "space": serial.SpaceParity}
	stops := map[string]serial.StopBits{"1": serial.OneStopBit, "1.5": serial.OnePointFiveStopBits, "2": serial.TwoStopBits}
	parity, ok := parities[c.Parity]
	if !ok {
		return nil, errors.New("串口校验位无效")
	}
	stop, ok := stops[c.StopBits]
	if !ok {
		return nil, errors.New("串口停止位无效")
	}
	bits := &serial.ModemOutputBits{DTR: true, RTS: true}
	if c.DTR != nil {
		bits.DTR = *c.DTR
	}
	if c.RTS != nil {
		bits.RTS = *c.RTS
	}
	return &serial.Mode{BaudRate: c.Baud, DataBits: c.DataBits, Parity: parity, StopBits: stop, InitialStatusBits: bits}, nil
}

type SerialPortInfo struct {
	Name    string `json:"name"`
	Product string `json:"product"`
	VID     string `json:"vid"`
	PID     string `json:"pid"`
	Busy    bool   `json:"busy"`
}

func (h *Hardware) serialPorts() ([]SerialPortInfo, error) {
	ports, err := enumerator.GetDetailedPortsList()
	out := []SerialPortInfo{}
	if err == nil {
		for _, p := range ports {
			out = append(out, SerialPortInfo{Name: p.Name, Product: p.Product, VID: p.VID, PID: p.PID})
		}
	} else {
		names, fallback := serial.GetPortsList()
		if fallback != nil {
			return nil, fallback
		}
		for _, name := range names {
			out = append(out, SerialPortInfo{Name: name})
		}
	}
	h.mu.Lock()
	for i := range out {
		for _, link := range h.links {
			if link.key == "serial:"+strings.ToUpper(out[i].Name) {
				out[i].Busy = true
			}
		}
	}
	h.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		a, e1 := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(out[i].Name), "COM"))
		b, e2 := strconv.Atoi(strings.TrimPrefix(strings.ToUpper(out[j].Name), "COM"))
		if e1 == nil && e2 == nil {
			return a < b
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
func (s *Store) hardwareEvents(id string, after int64, tail bool) ([]HardwareEvent, error) {
	query := "SELECT i.seq,direction,data,created,COALESCE(t.task_id,'') FROM hardware_io i LEFT JOIN hardware_io_tasks t ON t.seq=i.seq WHERE hardware_id=? AND i.seq>? ORDER BY i.seq LIMIT 250"
	if tail {
		query = "SELECT i.seq,direction,data,created,COALESCE(t.task_id,'') FROM (SELECT * FROM hardware_io WHERE hardware_id=? AND seq>? ORDER BY seq DESC LIMIT 250) i LEFT JOIN hardware_io_tasks t ON t.seq=i.seq ORDER BY i.seq"
	}
	rows, err := s.Query(query, id, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HardwareEvent{}
	for rows.Next() {
		var ev HardwareEvent
		if err = rows.Scan(&ev.Seq, &ev.Direction, &ev.Hex, &ev.Created, &ev.TaskID); err != nil {
			return nil, err
		}
		raw, _ := hex.DecodeString(ev.Hex)
		ev.Text = strings.ToValidUTF8(string(raw), "�")
		out = append(out, ev)
	}
	return out, rows.Err()
}
func (s *Server) hardwareEvents(w http.ResponseWriter, r *http.Request) {
	c, err := s.hardwareConfig(r)
	if err != nil {
		fail(w, 404, "连接配置不存在")
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	h := s.app.hardware
	h.notifyMu.Lock()
	changed := h.notify
	h.notifyMu.Unlock()
	events, err := s.app.store.hardwareEvents(c.ID, after, r.URL.Query().Get("tail") == "1")
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if len(events) == 0 && r.URL.Query().Get("wait") == "1" {
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-s.app.ctx.Done():
			return
		case <-timer.C:
		case <-changed:
		}
		events, err = s.app.store.hardwareEvents(c.ID, after, false)
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
	}
	connected, generation, controller, relay := h.connectionStatus(c.ID)
	title := ""
	if controller != "" {
		if t, e := s.app.store.task(controller); e == nil {
			title = t.Title
		}
	}
	jsonOut(w, 200, map[string]any{"events": events, "connected": connected, "connection_id": generation, "controller_task": controller, "controller_title": title, "relay": relay})
}

func (h *Hardware) connectionStatus(id string) (connected bool, generation, controller string, relay any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if link := h.links[id]; link != nil {
		connected, generation, controller = true, link.generation, link.controller
		// A long poll may have read a console config before the device became
		// a relay. Read status from the live connection's immutable settings.
		if link.relay != nil {
			relay = link.relay.snapshot()
		}
	}
	return
}

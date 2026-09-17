package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.bug.st/serial"
)

func TestSerialSettingsDefaultsAndValidation(t *testing.T) {
	mode, err := serialMode(HardwareConfig{Baud: 115200})
	if err != nil || mode.DataBits != 8 || mode.Parity != serial.NoParity || mode.StopBits != serial.OneStopBit || !mode.InitialStatusBits.DTR {
		t.Fatal(mode, err)
	}
	low := false
	mode, err = serialMode(HardwareConfig{Baud: 921600, DataBits: 7, Parity: "even", StopBits: "2", DTR: &low, RTS: &low})
	if err != nil || mode.DataBits != 7 || mode.Parity != serial.EvenParity || mode.StopBits != serial.TwoStopBits || mode.InitialStatusBits.DTR || mode.InitialStatusBits.RTS {
		t.Fatal(mode, err)
	}
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	request := toolsClient(t, a)
	for _, config := range []HardwareConfig{{DataBits: 9}, {Parity: "bad"}, {StopBits: "3"}} {
		config.Protocol = "serial"
		config.Name = "Invalid"
		config.Device = "COM999"
		config.Baud = 115200
		request("/api/tasks/"+task.ID+"/hardware", "POST", config, 400)
	}
	request("/api/tasks/"+task.ID+"/hardware", "POST", HardwareConfig{Name: "串口", Protocol: "serial", Device: "COM999", Baud: 115200, DataBits: 8, Parity: "none", StopBits: "1", DTR: &low, RTS: &low}, 200)
}
func TestHardwareNewGenerationRejectsStaleInput(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	request := toolsClient(t, a)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	wire := make(chan []byte, 4)
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 1024)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						wire <- append([]byte{}, buf[:n]...)
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	p, _ := strconv.Atoi(port)
	var cfg HardwareConfig
	_ = json.Unmarshal(request("/api/tasks/"+task.ID+"/hardware", "POST", HardwareConfig{Name: "test", Protocol: "tcp", Host: host, Port: p}, 200), &cfg)
	path := "/api/tasks/" + task.ID + "/hardware/" + cfg.ID
	var snapshot struct {
		Connection string `json:"connection_id"`
	}
	request(path+"/connect", "POST", map[string]string{}, 200)
	_ = json.Unmarshal(request(path+"/events", "GET", nil, 200), &snapshot)
	first := snapshot.Connection
	if first == "" {
		t.Fatal("missing generation")
	}
	request(path+"/disconnect", "POST", map[string]string{}, 200)
	request(path+"/connect", "POST", map[string]string{}, 200)
	_ = json.Unmarshal(request(path+"/events", "GET", nil, 200), &snapshot)
	if snapshot.Connection == first {
		t.Fatal("generation reused")
	}
	request(path+"/send", "POST", map[string]string{"encoding": "hex", "data": "ff", "connection_id": first}, 400)
	request(path+"/send", "POST", map[string]string{"encoding": "hex", "data": "09 1b 5b 41 03 0d", "connection_id": snapshot.Connection}, 200)
	select {
	case bytes := <-wire:
		if string(bytes) != "\t\x1b[A\x03\r" {
			t.Fatalf("wrong bytes %x", bytes)
		}
	case <-time.After(time.Second):
		t.Fatal("no bytes")
	}
}
func TestHardwareTailAndLongPoll(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	var cfg HardwareConfig
	request := toolsClient(t, a)
	_ = json.Unmarshal(request("/api/tasks/"+task.ID+"/hardware", "POST", HardwareConfig{Name: "test", Protocol: "tcp", Host: "127.0.0.1", Port: 1}, 200), &cfg)
	// Record a split UTF-8 character exactly; terminal clients consume raw bytes.
	a.hardware.record(cfg.ID, "rx", []byte{0xe4})
	a.hardware.record(cfg.ID, "rx", []byte{0xb8, 0xad})
	evs, err := a.store.hardwareEvents(cfg.ID, 0, false)
	if err != nil || evs[0].Hex != "e4" || evs[1].Hex != "b8ad" {
		t.Fatal(evs, err)
	}
	for i := 0; i < 260; i++ {
		a.hardware.record(cfg.ID, "rx", []byte("x"))
	}
	tail, err := a.store.hardwareEvents(cfg.ID, 0, true)
	if err != nil || len(tail) != 250 || tail[0].Seq <= evs[1].Seq {
		t.Fatal(len(tail), err)
	}
	srv := httptest.NewServer((&Server{app: a}).Handler())
	defer srv.Close()
	path := srv.URL + "/api/tasks/" + task.ID + "/hardware/" + cfg.ID + "/events?wait=1&after=" + strconv.FormatInt(tail[len(tail)-1].Seq, 10)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", path, nil)
	req.AddCookie(&http.Cookie{Name: "jianzuo_session", Value: "tools-test"})
	go func() { time.Sleep(40 * time.Millisecond); a.hardware.record(cfg.ID, "rx", []byte("prompt# ")) }()
	started := time.Now()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(body), "prompt# ") || time.Since(started) > 700*time.Millisecond {
		t.Fatal(string(body), time.Since(started))
	}
}

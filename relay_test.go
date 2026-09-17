package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestRelayProtocolAndFeedback(t *testing.T) {
	for _, v := range []struct {
		ch   int
		op   byte
		want []byte
	}{{1, 3, []byte{0xa0, 1, 3, 0xa4}}, {1, 2, []byte{0xa0, 1, 2, 0xa3}}, {1, 5, []byte{0xa0, 1, 5, 0xa6}}, {254, 3, []byte{0xa0, 254, 3, 0xa1}}} {
		if !bytes.Equal(relayFrame(v.ch, v.op), v.want) {
			t.Fatal(v)
		}
	}
	s := newRelaySession(RelayConfig{Channel: 1, Contact: "no"})
	s.receive([]byte{0, 0xa0, 2, 1, 0xa3, 0xa0, 1, 1, 0x00, 0xa0})
	s.receive([]byte{1})
	s.receive([]byte{1, 0xa2})
	v := s.snapshot()
	if v["known"] != true || v["power"] != true {
		t.Fatal(v)
	}
	ncConfig := RelayConfig{Channel: 1, Contact: "nc"}
	nc := newRelaySession(ncConfig)
	ncConfig.Contact = "no"
	nc.receive(relayFrame(1, 1))
	v = nc.snapshot()
	if v["power"] != false {
		t.Fatal("NC power inversion", v)
	}
	for _, c := range []HardwareConfig{{Kind: "relay"}, {Kind: "relay", Protocol: "serial", Baud: 9600, Relay: &RelayConfig{Channel: 1, Contact: "", CycleSeconds: 3}}, {Kind: "relay", Protocol: "serial", Baud: 115200, Relay: &RelayConfig{Channel: 1, Contact: "no", CycleSeconds: 3}}} {
		if validateRelay(c) == nil {
			t.Fatal("accepted invalid relay", c)
		}
	}
}

func TestRelayCommandsCycleAndExclusion(t *testing.T) {
	for _, contact := range []string{"no", "nc"} {
		t.Run(contact, func(t *testing.T) { testRelayCommandsCycleAndExclusion(t, contact) })
	}
}

func testRelayCommandsCycleAndExclusion(t *testing.T, contact string) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	other := taskFor(t, a)
	request := toolsClient(t, a)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	wire := make(chan []byte, 20)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		coil := byte(0)
		for {
			b := make([]byte, 4)
			if _, e = io.ReadFull(conn, b); e != nil {
				return
			}
			wire <- b
			if b[2] == 2 {
				coil = 0
			}
			if b[2] == 3 {
				coil = 1
			}
			reply := relayFrame(1, coil)
			conn.Write(reply[:1])
			conn.Write(reply[1:])
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	p, _ := strconv.Atoi(port)
	c := HardwareConfig{Name: "电源模拟", Kind: "relay", Protocol: "tcp", Host: host, Port: p, Relay: &RelayConfig{Channel: 1, Contact: contact, CycleSeconds: 1}}
	onOp, offOp := byte(3), byte(2)
	if contact == "nc" {
		onOp, offOp = 2, 3
	}
	json.Unmarshal(request("/api/tasks/"+task.ID+"/hardware", "POST", c, 200), &c)
	path := "/api/tasks/" + task.ID + "/hardware/" + c.ID
	request(path+"/connect", "POST", map[string]string{}, 200)
	var state map[string]any
	json.Unmarshal(request(path+"/events", "GET", nil, 200), &state)
	generation := state["connection_id"].(string)
	request(path+"/send", "POST", map[string]string{"encoding": "hex", "data": "a00101a2"}, 400)
	request(path+"/relay", "POST", map[string]any{"action": "on", "connection_id": generation}, 400)
	request(path+"/relay", "POST", map[string]any{"action": "on", "connection_id": generation, "confirmed": true}, 200)
	if b := <-wire; !bytes.Equal(b, relayFrame(1, onOp)) {
		t.Fatal(b)
	}
	done := make(chan error, 1)
	go func() { done <- a.hardware.operateRelay(context.Background(), c, task.ID, generation, "cycle") }()
	if b := <-wire; !bytes.Equal(b, relayFrame(1, offOp)) {
		t.Fatal(b)
	}
	if e := a.hardware.control(c.ID, other.ID, generation, true); e == nil {
		t.Fatal("control stolen during power cycle")
	}
	if e := a.hardware.disconnectOwned(c.ID, task.ID, generation); e == nil {
		t.Fatal("disconnected during power cycle")
	}
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if b := <-wire; !bytes.Equal(b, relayFrame(1, onOp)) {
		t.Fatal(b)
	}
	// Mutating a request config cannot change the active connection's wiring.
	c.Relay.Contact = "nc"
	if contact == "nc" {
		c.Relay.Contact = "no"
	}
	if e := a.hardware.operateRelay(context.Background(), c, task.ID, generation, "on"); e != nil {
		t.Fatal(e)
	}
	if b := <-wire; !bytes.Equal(b, relayFrame(1, onOp)) {
		t.Fatal("request changed live wiring", b)
	}
}

func TestRelayEventsWithStaleConsoleConfig(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	request := toolsClient(t, a)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		io.Copy(io.Discard, conn)
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	p, _ := strconv.Atoi(port)
	c := HardwareConfig{Name: "控制台改继电器", Protocol: "tcp", Host: host, Port: p}
	json.Unmarshal(request("/api/tasks/"+task.ID+"/hardware", "POST", c, 200), &c)
	path := "/api/tasks/" + task.ID + "/hardware/" + c.ID
	// Deterministically model a request that read the old console config,
	// then observed a newly connected relay after its long-poll wait.
	c.Kind = "relay"
	c.Relay = &RelayConfig{Channel: 1, Contact: "nc", CycleSeconds: 1}
	if err := a.hardware.connectOwned(c, "", task.ID); err != nil {
		t.Fatal(err)
	}
	a.hardware.mu.Lock()
	a.hardware.links[c.ID].relay.receive(relayFrame(1, 0))
	a.hardware.mu.Unlock()
	var state struct {
		Connected  bool   `json:"connected"`
		Generation string `json:"connection_id"`
		Relay      struct {
			Known bool `json:"known"`
			Power bool `json:"power"`
		} `json:"relay"`
	}
	if err := json.Unmarshal(request(path+"/events", "GET", nil, 200), &state); err != nil {
		t.Fatal(err)
	}
	if !state.Connected || !state.Relay.Known || !state.Relay.Power {
		t.Fatalf("wrong live relay state: %+v", state)
	}
	// Both status and mutation must still acquire the hardware lock afterwards.
	request(path+"/events", "GET", nil, 200)
	request(path+"/release", "POST", map[string]string{"connection_id": state.Generation}, 200)
}

func TestRelayTimeoutDoesNotRetryOrContinueCycle(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	wire := make(chan []byte, 4)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		for {
			b := make([]byte, 4)
			if _, e = io.ReadFull(c, b); e != nil {
				return
			}
			wire <- b
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	p, _ := strconv.Atoi(port)
	c := HardwareConfig{ID: "timeout", Kind: "relay", Protocol: "tcp", Host: host, Port: p, Relay: &RelayConfig{Channel: 1, Contact: "no", CycleSeconds: 1}}
	if e := a.hardware.connectOwned(c, "", task.ID); e != nil {
		t.Fatal(e)
	}
	a.hardware.mu.Lock()
	generation := a.hardware.links[c.ID].generation
	a.hardware.mu.Unlock()
	if e := a.hardware.operateRelay(context.Background(), c, task.ID, generation, "cycle"); e == nil {
		t.Fatal("missing feedback accepted")
	}
	select {
	case b := <-wire:
		if !bytes.Equal(b, relayFrame(1, 2)) {
			t.Fatal(b)
		}
	default:
		t.Fatal("off missing")
	}
	select {
	case b := <-wire:
		t.Fatal("unexpected retry/on", b)
	case <-time.After(30 * time.Millisecond):
	}
}

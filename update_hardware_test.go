package main

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"
)

type updateHardwareConnection struct {
	writes  atomic.Int32
	closed  atomic.Bool
	started chan struct{}
	finish  chan struct{}
}

func (c *updateHardwareConnection) Read([]byte) (int, error) { return 0, io.EOF }
func (c *updateHardwareConnection) Close() error             { c.closed.Store(true); return nil }
func (c *updateHardwareConnection) Write(b []byte) (int, error) {
	c.writes.Add(1)
	if c.started != nil {
		close(c.started)
		<-c.finish
	}
	return len(b), nil
}

func TestUpdateAllowsIdleHardwareWithoutDisconnecting(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	conn := &updateHardwareConnection{}
	link := &hardwareLink{conn: conn, generation: "synthetic-generation", controller: "synthetic-task", relay: newRelaySession(RelayConfig{Channel: 1, Contact: "no", CycleSeconds: 1})}
	a.hardware.links["synthetic-device"] = link
	release, err := a.beginUpdate()
	if err != nil {
		t.Fatal("idle connection blocked update", err)
	}
	if conn.closed.Load() || a.hardware.links["synthetic-device"] != link {
		t.Fatal("preparation disconnected hardware")
	}
	if err = a.hardware.sendOwned("synthetic-device", link.generation, link.controller, []byte("blocked")); !errors.Is(err, errUpdateBusy) {
		t.Fatal("write bypassed update admission", err)
	}
	cfg := HardwareConfig{ID: "synthetic-device", Protocol: "tcp", Kind: "relay", Relay: &RelayConfig{Channel: 1, Contact: "no", CycleSeconds: 1}}
	if err = a.hardware.operateRelay(context.Background(), cfg, link.controller, link.generation, "on"); !errors.Is(err, errUpdateBusy) {
		t.Fatal("relay bypassed update admission", err)
	}
	if conn.writes.Load() != 0 || link.relay.isBusy() {
		t.Fatal("maintenance request touched hardware")
	}
	release()
	if err = a.hardware.sendOwned("synthetic-device", link.generation, link.controller, []byte("resumed")); err != nil {
		t.Fatal("preparation failure did not restore existing connection", err)
	}
	if conn.closed.Load() || conn.writes.Load() != 1 {
		t.Fatal("connection did not survive cancelled preparation")
	}
	// A real preparation failure must preserve this same live connection too.
	if err = a.store.set("update_repository", "owner/jianzuo"); err != nil {
		t.Fatal(err)
	}
	s := &Server{app: a}
	if _, err = s.prepareUpdate(context.Background(), testUpdateChecker(t, 500, "")); err == nil {
		t.Fatal("preparation unexpectedly succeeded")
	}
	if a.updating.Load() || conn.closed.Load() || a.hardware.links["synthetic-device"] != link {
		t.Fatal("failed preparation lost idle hardware")
	}
}

func TestUpdateRejectsInFlightHardwareWriteAndRelay(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	conn := &updateHardwareConnection{started: make(chan struct{}), finish: make(chan struct{})}
	t.Cleanup(func() {
		select {
		case <-conn.finish:
		default:
			close(conn.finish)
		}
	})
	link := &hardwareLink{conn: conn, generation: "synthetic-generation", controller: "synthetic-task"}
	a.hardware.links["synthetic-device"] = link
	result := make(chan error, 1)
	go func() {
		result <- a.hardware.sendOwned("synthetic-device", link.generation, link.controller, []byte("synthetic"))
	}()
	select {
	case <-conn.started:
	case <-time.After(time.Second):
		t.Fatal("synthetic write did not start")
	}
	release, err := a.beginUpdate()
	close(conn.finish)
	if writeErr := <-result; writeErr != nil {
		t.Fatal(writeErr)
	}
	if release != nil || !errors.Is(err, errUpdateBusy) || a.updating.Load() || conn.closed.Load() {
		t.Fatal("active write was interrupted or accepted", err)
	}
	link.relay = newRelaySession(RelayConfig{Channel: 1, Contact: "no", CycleSeconds: 1})
	link.relay.busy = true
	release, err = a.beginUpdate()
	if release != nil || !errors.Is(err, errUpdateBusy) || conn.closed.Load() {
		t.Fatal("active relay was interrupted or accepted", err)
	}
	link.relay.busy = false
	release, err = a.beginUpdate()
	if err != nil {
		t.Fatal("finished operation still blocks update", err)
	}
	release()
}

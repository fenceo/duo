package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func desktopFixture(t *testing.T) *DesktopControl {
	d := newDesktopControl()
	d.targets = func() ([]DesktopTarget, error) {
		return []DesktopTarget{{ID: "desktop", Title: "Synthetic desktop"}}, nil
	}
	d.capture = func(target string) (desktopObservation, error) {
		return desktopObservation{Data: []byte("synthetic-png"), Bounds: DesktopBounds{Width: 100, Height: 80}, Input: 10, Foreground: 1, Target: target}, nil
	}
	d.input = func(desktopObservation, DesktopAction) error { return nil }
	return d
}
func TestDesktopGrantObserveActAndRevocation(t *testing.T) {
	d := desktopFixture(t)
	ctx := context.Background()
	if _, err := d.observe(ctx, "task", ""); err == nil {
		t.Fatal("unshared capture")
	}
	if err := d.grant("task", "desktop", false); err != nil {
		t.Fatal(err)
	}
	lease := d.lease("task")
	frame, err := d.observe(ctx, "task", lease)
	if err != nil {
		t.Fatal(err)
	}
	action := DesktopAction{Frame: frame.ID, Action: "click", X: 12, Y: 14}
	if err = d.act(ctx, "task", lease, action); err == nil {
		t.Fatal("read only input")
	}
	if err = d.grant("other", "desktop", true); err == nil {
		t.Fatal("another task stole desktop")
	}
	if err = d.grant("task", "desktop", true); err != nil {
		t.Fatal(err)
	}
	if _, err = d.observe(ctx, "task", lease); err == nil {
		t.Fatal("old authorization revived")
	}
	lease = d.lease("task")
	frame, err = d.observe(ctx, "task", lease)
	if err != nil {
		t.Fatal(err)
	}
	action.Frame = frame.ID
	calls := 0
	d.input = func(ob desktopObservation, v DesktopAction) error {
		calls++
		if ob.Bounds.Width != 100 || v.X != 12 {
			t.Error("observation lost")
		}
		return nil
	}
	if err = d.act(ctx, "task", lease, action); err != nil {
		t.Fatal(err)
	}
	if err = d.act(ctx, "task", lease, action); err == nil || calls != 1 {
		t.Fatal("frame reused", err, calls)
	}
	d.revoke()
	if _, err = d.observe(ctx, "task", lease); err == nil {
		t.Fatal("revoked capture")
	}
	if d.status().Active || len(d.observation.Data) != 0 {
		t.Fatal("revocation retained frame")
	}
}
func TestDesktopStaleHumanInputAndBounds(t *testing.T) {
	d := desktopFixture(t)
	d.grant("task", "desktop", true)
	ctx := context.Background()
	frame, _ := d.observe(ctx, "task", "")
	d.observed = time.Now().Add(-31 * time.Second)
	if err := d.act(ctx, "task", "", DesktopAction{Frame: frame.ID, Action: "key", Key: "enter"}); err == nil {
		t.Fatal("stale frame")
	}
	frame, _ = d.observe(ctx, "task", "")
	if err := d.act(ctx, "task", "", DesktopAction{Frame: frame.ID, Action: "click", X: 100, Y: 1}); err == nil {
		t.Fatal("outside bounds")
	}
	frame, _ = d.observe(ctx, "task", "")
	d.input = func(desktopObservation, DesktopAction) error { return errors.New("用户已经接管") }
	if err := d.act(ctx, "task", "", DesktopAction{Frame: frame.ID, Action: "type", Text: "fixture"}); err == nil {
		t.Fatal("human takeover ignored")
	}
	if d.frame != "" {
		t.Fatal("failed action frame could replay")
	}
	d.state.Expires = now() - 1
	if d.status().Active {
		t.Fatal("expired share")
	}
	for _, key := range []string{"ctrl+ctrl+c", "shell:cmd", "ctrl+", "a+b"} {
		if _, err := desktopKeyCodes(key); err == nil {
			t.Fatal(key)
		}
	}
	for _, key := range []string{"ctrl+c", "alt+tab", "escape", "shift+home"} {
		if _, err := desktopKeyCodes(key); err != nil {
			t.Fatal(err)
		}
	}
}
func TestDesktopHTTPAndMCPLease(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	a.desktop = desktopFixture(t)
	one, two := taskFor(t, a), taskFor(t, a)
	request := toolsClient(t, a)
	request("/api/tasks/"+one.ID+"/desktop/frame", "POST", map[string]any{}, 409)
	request("/api/tasks/"+one.ID+"/desktop", "PUT", map[string]any{"target": "desktop", "control": true}, 200)
	request("/api/tasks/"+one.ID+"/desktop/action", "POST", map[string]any{"action": "click", "frame": "synthetic"}, 400)
	request("/api/tasks/"+two.ID+"/desktop/frame", "POST", map[string]any{}, 409)
	cfg := a.config.get()
	cfg.Listen = "127.0.0.1:12345"
	a.hardwareAddress = cfg.Listen
	runtime, release, err := a.prepareHardwareAI(context.Background(), one, "fixture-run", cfg)
	if err != nil || runtime == nil {
		t.Fatal(err)
	}
	defer release()
	lease := a.hardwareAI.lease(runtime.Token)
	if lease == nil || lease.desktop == "" {
		t.Fatal("desktop lease missing without hardware")
	}
	content, err := a.callDesktopTool(context.Background(), lease, "desktop_observe", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(content)
	if !strings.Contains(string(data), `"type":"image"`) || !strings.Contains(string(data), `\"frame\"`) {
		t.Fatal(string(data))
	}
	request("/api/desktop", "DELETE", nil, 200)
	if _, err = a.callDesktopTool(context.Background(), lease, "desktop_observe", json.RawMessage(`{}`)); err == nil {
		t.Fatal("revocation did not revoke AI")
	}
}

package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestDesktopHumanLeaseAndAIIsolation(t *testing.T) {
	d := desktopFixture(t)
	ctx := context.Background()
	if _, err := d.takeHuman("t"); err == nil {
		t.Fatal("unshared control")
	}
	if err := d.grant("t", "desktop", false); err != nil {
		t.Fatal(err)
	}
	lease := d.lease("t")
	aiFrame, _ := d.observe(ctx, "t", lease)
	token, err := d.takeHuman("t")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = d.observe(ctx, "t", lease); err == nil {
		t.Fatal("AI observed during human control")
	}
	if err = d.act(ctx, "t", lease, DesktopAction{Frame: aiFrame.ID, Action: "click", X: 2, Y: 3}); err == nil {
		t.Fatal("AI took human control")
	}
	if _, err = d.observeAs(ctx, "other", "", token); err == nil {
		t.Fatal("cross-task control")
	}
	if _, err = d.observeAs(ctx, "t", "", "wrong"); err == nil {
		t.Fatal("wrong view token")
	}
	frame, err := d.observeAs(ctx, "t", "", token)
	if err != nil {
		t.Fatal(err)
	}
	// A refresh in flight must not invalidate the image still being displayed.
	if _, err = d.observeAs(ctx, "t", "", token); err != nil {
		t.Fatal(err)
	}
	calls := 0
	d.input = func(ob desktopObservation, v DesktopAction) error {
		calls++
		if !ob.Human {
			t.Fatal("human flag missing")
		}
		return nil
	}
	act := DesktopAction{Frame: frame.ID, Action: "click", X: 12, Y: 14}
	if err = d.actAs(ctx, "t", "", token, act); err != nil {
		t.Fatal(err)
	}
	if err = d.actAs(ctx, "t", "", token, act); err == nil || calls != 1 {
		t.Fatal("replayed action", err, calls)
	}
	// Another view can explicitly take over; a late old-view release cannot
	// revoke the new controller, nor can it replay its frame.
	second, err := d.takeHuman("t")
	if err != nil {
		t.Fatal(err)
	}
	if err = d.releaseHuman("t", token); err == nil {
		t.Fatal("stale release")
	}
	if _, err = d.observeAs(ctx, "t", "", token); err == nil {
		t.Fatal("stale controller")
	}
	if err = d.releaseHuman("t", second); err != nil {
		t.Fatal(err)
	}
	if _, err = d.observe(ctx, "t", lease); err != nil {
		t.Fatal("AI could not observe after release", err)
	}
	if d.state.Control {
		t.Fatal("manual control elevated AI permission")
	}
	token, _ = d.takeHuman("t")
	d.humanExpires = time.Now().Add(-time.Second)
	if d.status().HumanControl {
		t.Fatal("abandoned view not expired")
	}
	if _, err = d.observeAs(ctx, "t", "", token); err == nil {
		t.Fatal("expired token")
	}
}

func TestDesktopHumanDoesNotCrossGrantOrAcceptAIFlags(t *testing.T) {
	d := desktopFixture(t)
	d.grant("t", "desktop", true)
	token, _ := d.takeHuman("t")
	d.grant("t", "desktop", true)
	if _, err := d.observeAs(context.Background(), "t", "", token); err == nil {
		t.Fatal("old grant survives replacement")
	}
	for _, raw := range []string{`{"action":"key","key":"enter","human":true}`, `{"action":"key","key":"enter","token":"fake"}`} {
		var action DesktopAction
		if json.Unmarshal([]byte(raw), &action) == nil {
			t.Fatal("AI input bypass flag accepted")
		}
	}
}

func TestDesktopHumanHTTP(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	a.desktop = desktopFixture(t)
	task := taskFor(t, a)
	request := toolsClient(t, a)
	base := "/api/tasks/" + task.ID + "/desktop"
	request(base, "PUT", map[string]any{"target": "desktop", "control": false}, 200)
	request(base+"/control", "POST", map[string]any{"enabled": true}, 200)
	token := a.desktop.humanToken
	request(base+"/control/frame", "POST", map[string]any{}, 400)
	request(base+"/control/frame", "POST", map[string]any{"token": "wrong"}, 409)
	request(base+"/control/frame", "POST", map[string]any{"token": token}, 200)
	frame := a.desktop.frame
	request(base+"/control/action", "POST", map[string]any{"token": token, "action": map[string]any{"frame": frame, "action": "click", "x": 12, "y": 14}}, 200)
	request(base+"/control/action", "POST", map[string]any{"token": token, "action": map[string]any{"frame": frame, "action": "click", "x": 12, "y": 14}}, 409)
	request(base+"/control", "POST", map[string]any{"enabled": false, "token": token}, 200)
	request(base+"/control/frame", "POST", map[string]any{"token": token}, 409)
}

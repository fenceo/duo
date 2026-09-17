package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/larksuite/oapi-sdk-go/v3/scene/registration"
	"strings"
	"testing"
	"time"
)

func fakeRegistration() *registration.RegisterAppResult {
	return &registration.RegisterAppResult{ClientID: "cli_test", ClientSecret: "private-secret-never-expose", UserInfo: &registration.UserInfo{OpenID: "owner", TenantBrand: "feishu"}}
}
func mockSetup(t *testing.T) (*App, *Feishu) {
	t.Helper()
	a := fixture(t, &fakeRunner{})
	f := a.feishu
	f.startRegistered = func() {}
	f.initializeMenu = func(context.Context, FeishuConfig) error { return nil }
	f.welcome = func(context.Context, FeishuConfig, string) (string, error) { return "test-chat", nil }
	return a, f
}
func awaitSetup(t *testing.T, f *Feishu, id, phase string) FeishuSetup {
	t.Helper()
	waitUntil(t, func() bool { s, _ := f.setupState("session", id); return s.Phase == phase })
	s, _ := f.setupState("session", id)
	return s
}
func TestScanBindsVerifiedIdentityAndTask(t *testing.T) {
	a, f := mockSetup(t)
	task := taskFor(t, a)
	gate := make(chan struct{})
	f.register = func(ctx context.Context, o *registration.Options) (*registration.RegisterAppResult, error) {
		if !o.CreateOnly || o.AppID != "" || o.Addons.Preset == nil || *o.Addons.Preset {
			return nil, errors.New("unsafe registration options")
		}
		o.OnQRCode(&registration.QRCodeInfo{URL: "https://open.feishu.cn/page/launcher?token=short-lived", ExpireIn: 600})
		select {
		case <-gate:
			return fakeRegistration(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	s, err := f.beginSetup("session", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	qr := awaitSetup(t, f, s.ID, "scanning")
	if !strings.HasPrefix(qr.QR, "data:image/png;base64,") {
		t.Fatal("no QR")
	}
	if _, err = f.setupState("another-session", s.ID); err == nil {
		t.Fatal("session leak")
	}
	if _, err = f.beginSetup("session", task.ID); err == nil {
		t.Fatal("duplicate accepted")
	}
	close(gate)
	complete := awaitSetup(t, f, s.ID, "complete")
	cfg := a.config.get().Feishu
	if cfg.Owner != "owner" || cfg.Secret != fakeRegistration().ClientSecret || !cfg.Enabled || a.bound("test-chat") != task.ID {
		t.Fatal("registration not committed")
	}
	raw, _ := json.Marshal(complete)
	if strings.Contains(string(raw), cfg.Secret) || complete.QR != "" || complete.URL != "" {
		t.Fatal("secret or QR retained")
	}
	if _, err = f.beginSetup("session", ""); err == nil {
		t.Fatal("existing app overwritten")
	}
	if err = f.receiveMenu("menu1", "stranger", "jianzuo.tasks"); err != nil {
		t.Fatal(err)
	}
	var count int
	a.store.QueryRow("SELECT count(*) FROM outbox").Scan(&count)
	if count != 0 {
		t.Fatal("unpaired menu accepted")
	}
	f.receiveMenu("menu1", "owner", "jianzuo.tasks")
	f.receiveMenu("menu1", "owner", "jianzuo.tasks")
	a.store.QueryRow("SELECT count(*) FROM outbox").Scan(&count)
	if count != 1 {
		t.Fatal("menu not deduplicated", count)
	}
}
func TestCancelledScanCannotCommitLateResult(t *testing.T) {
	a, f := mockSetup(t)
	gate := make(chan struct{})
	finished := make(chan struct{})
	f.register = func(context.Context, *registration.Options) (*registration.RegisterAppResult, error) {
		<-gate
		defer close(finished)
		return fakeRegistration(), nil
	}
	s, err := f.beginSetup("session", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.cancelSetup("other", s.ID); err == nil {
		t.Fatal("other session cancelled")
	}
	if err = f.cancelSetup("session", s.ID); err != nil {
		t.Fatal(err)
	}
	close(gate)
	<-finished
	// Closing the app waits for the late completion goroutine before assertions.
	a.close()
	if a.config.get().Feishu.AppID != "" {
		t.Fatal("cancelled result committed")
	}
}
func TestScanPartialAndExpiredStates(t *testing.T) {
	a, f := mockSetup(t)
	f.register = func(context.Context, *registration.Options) (*registration.RegisterAppResult, error) {
		return fakeRegistration(), nil
	}
	f.initializeMenu = func(context.Context, FeishuConfig) error { return errors.New("provider rejected secret=DO_NOT_SHOW") }
	f.welcome = func(context.Context, FeishuConfig, string) (string, error) { return "", errors.New("pending approval") }
	s, _ := f.beginSetup("session", "")
	partial := awaitSetup(t, f, s.ID, "partial")
	if a.config.get().Feishu.Owner != "owner" || strings.Contains(partial.Menu, "DO_NOT_SHOW") {
		t.Fatal("partial state unsafe")
	}
	_, g := mockSetup(t)
	g.register = func(context.Context, *registration.Options) (*registration.RegisterAppResult, error) {
		return nil, context.DeadlineExceeded
	}
	e, _ := g.beginSetup("session", "")
	failed := awaitSetup(t, g, e.ID, "failed")
	if !strings.Contains(failed.Message, "过期") {
		t.Fatal(failed)
	}
	if failed.Expires < time.Now().UnixMilli() {
		t.Fatal("invalid initial expiration")
	}
}

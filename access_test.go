package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPanelURLsValidationAndTaskLinks(t *testing.T) {
	for _, bad := range []string{"javascript:alert(1)", "http://user:pass@host", "http://host/?token=secret", "http://host/path", "http://host/#x", "//host"} {
		v := AccessConfig{LAN: bad}
		if normalizeAccess(&v) == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	c := a.config.get()
	c.Access = AccessConfig{LAN: "http://192.168.50.10:8789/", Tailscale: "http://100.100.10.10:8789"}
	if e := a.config.save(c); e != nil {
		t.Fatal(e)
	}
	card, e := a.feishu.taskDetailCard("chat", task)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(card)
	for _, want := range []string{"http://192.168.50.10:8789/?task=" + task.ID, "http://100.100.10.10:8789/?task=" + task.ID} {
		if !strings.Contains(string(b), want) {
			t.Fatal("missing deep link", want)
		}
	}
	card, e = a.feishu.taskListCard("chat", "", 0)
	if e != nil {
		t.Fatal(e)
	}
	b, _ = json.Marshal(card)
	if !strings.Contains(string(b), "http://100.100.10.10:8789/") || strings.Contains(string(b), "?task=") {
		t.Fatal("list link should open home")
	}
}

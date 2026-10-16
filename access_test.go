package main

import "testing"

func TestApplyListenAccess(t *testing.T) {
	got, err := applyListenAccess("127.0.0.1:8789", "0.0.0.0:8789")
	if err != nil || got != "0.0.0.0:8789" {
		t.Fatal(got, err)
	}
	got, err = applyListenAccess("0.0.0.0:8789", "127.0.0.1:8789")
	if err != nil || got != "127.0.0.1:8789" {
		t.Fatal(got, err)
	}
	if _, err = applyListenAccess("127.0.0.1:8789", "0.0.0.0:9999"); err == nil {
		t.Fatal("port change accepted")
	}
	if _, err = applyListenAccess("127.0.0.1:8789", "192.168.1.2:8789"); err == nil {
		t.Fatal("arbitrary host accepted")
	}
	if !listenAllowsRemote("0.0.0.0:8789") || listenAllowsRemote("127.0.0.1:8789") {
		t.Fatal("remote detection wrong")
	}
}

func TestCodexApprovalDecisionList(t *testing.T) {
	if got := codexApprovalDecisionList(CodexPendingRequest{Method: "item/permissions/requestApproval", Params: []byte(`{}`)}); len(got) != 2 {
		t.Fatal(got)
	}
	got := codexApprovalDecisionList(CodexPendingRequest{Method: "item/commandExecution/requestApproval", Params: []byte(`{"availableDecisions":["accept","decline"]}`)})
	if len(got) != 2 || got[0] != "accept" {
		t.Fatal(got)
	}
	got = codexApprovalDecisionList(CodexPendingRequest{Method: harnessPermissionMethod, Params: []byte(harnessPermissionTestParams)})
	if len(got) != 3 {
		t.Fatal(got)
	}
}

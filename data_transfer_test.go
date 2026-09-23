package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactWorkspaceTextRemovesSecretsAndMachinePaths(t *testing.T) {
	input := `token=sk-abcdefghijklmnop C:\Users\alice\project /home/alice/project -----BEGIN RSA PRIVATE KEY----- secret -----END RSA PRIVATE KEY-----`
	got := redactWorkspaceText(input)
	for _, want := range []string{"sk-abcdefghijklmnop", `C:\Users\alice\project`, "/home/alice/project", "BEGIN RSA PRIVATE KEY"} {
		if strings.Contains(got, want) {
			t.Fatalf("redacted text still contains %q: %q", want, got)
		}
	}
}

func TestWorkspaceZipRoundTripContainsNoAttachments(t *testing.T) {
	a := workspaceArchive{
		Protocol: workspaceArchiveProtocol,
		Kind:     workspaceArchiveKind,
		Tasks:    []workspaceArchiveTask{{ID: "task-1", Title: "demo"}},
	}
	raw, err := buildWorkspaceZip(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readWorkspaceZip(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Archive.Attachments) != 0 {
		t.Fatalf("archive unexpectedly contains attachments: %#v", got.Archive.Attachments)
	}
	if len(got.Archive.Tasks) != 1 || got.Archive.Tasks[0].ID != "task-1" {
		t.Fatalf("archive task was not preserved: %#v", got.Archive.Tasks)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 1 || zr.File[0].Name != "workspace.json" {
		t.Fatalf("unexpected export entries: %#v", zr.File)
	}
}

func TestWorkspaceZipRejectsAttachmentEntry(t *testing.T) {
	a := workspaceArchive{Protocol: workspaceArchiveProtocol, Kind: workspaceArchiveKind}
	index, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	f, err := zw.Create("workspace.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(index); err != nil {
		t.Fatal(err)
	}
	f, err = zw.Create("attachments/secret.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte("credential")); err != nil {
		t.Fatal(err)
	}
	if err = zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = readWorkspaceZip(out.Bytes()); err == nil || !strings.Contains(err.Error(), "不支持附件") {
		t.Fatalf("expected attachment rejection, got %v", err)
	}
}

func TestBuildWorkspaceZipRejectsAttachmentManifest(t *testing.T) {
	a := workspaceArchive{
		Protocol:    workspaceArchiveProtocol,
		Kind:        workspaceArchiveKind,
		Attachments: []workspaceArchiveAttachment{{ID: "a1", Name: "secret.txt"}},
	}
	if _, err := buildWorkspaceZip(a, map[string][]byte{"a1": []byte("credential")}); err == nil || !strings.Contains(err.Error(), "不支持附件") {
		t.Fatalf("expected export attachment rejection, got %v", err)
	}
}

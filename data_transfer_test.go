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

func TestWorkspaceWorkbenchRoundTripAndRedaction(t *testing.T) {
	a := workspaceArchive{
		Protocol: workspaceArchiveProtocol,
		Kind:     workspaceArchiveKind,
		Workbench: workspaceArchiveWorkbench{
			Modes:    []WorkMode{{ID: "release", Name: "Release", Permission: "workspace", Prompt: "token=sk-abcdefghijklmnop C:\\Users\\alice\\repo"}},
			Commands: []QuickCommand{{ID: "ship", Name: "Ship", Content: "cd /home/alice/repo && git status"}},
		},
	}
	raw, err := buildWorkspaceZip(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readWorkspaceZip(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Archive.Workbench.Modes) != 1 || len(got.Archive.Workbench.Commands) != 1 {
		t.Fatalf("workbench catalog was not preserved: %#v", got.Archive.Workbench)
	}
	for _, text := range []string{got.Archive.Workbench.Modes[0].Prompt, got.Archive.Workbench.Commands[0].Content} {
		if strings.Contains(text, "sk-abcdefghijklmnop") || strings.Contains(text, "C:\\Users\\alice") || strings.Contains(text, "/home/alice") {
			t.Fatalf("workbench text was not redacted: %q", text)
		}
	}
}

func TestMergeWorkspaceWorkbenchRenamesCollisionsWithoutOverwrite(t *testing.T) {
	current := WorkCatalog{
		Modes:    []WorkMode{{ID: "release", Name: "Existing", Permission: "workspace", Approval: "request", AllowNetwork: boolPtr(true)}},
		Commands: []QuickCommand{{ID: "ship", Name: "Existing", Content: "keep"}},
	}
	imported := workspaceArchiveWorkbench{
		Modes:    []WorkMode{{ID: "release", Name: "Imported", Permission: "workspace", Approval: "request", AllowNetwork: boolPtr(true)}},
		Commands: []QuickCommand{{ID: "ship", Name: "Imported", Content: "replace?"}},
	}
	merged, modeMap, err := mergeWorkspaceWorkbench(current, imported)
	if err != nil {
		t.Fatal(err)
	}
	if modeMap["release"] == "release" || !strings.HasPrefix(modeMap["release"], "release-imported-") {
		t.Fatalf("collision was not remapped: %#v", modeMap)
	}
	if len(merged.Modes) != 2 || merged.Modes[0].Name != "Existing" || merged.Modes[1].Name != "Imported" {
		t.Fatalf("existing mode was overwritten: %#v", merged.Modes)
	}
	if len(merged.Commands) != 2 || merged.Commands[0].Content != "keep" || merged.Commands[1].Content != "replace?" {
		t.Fatalf("existing command was overwritten: %#v", merged.Commands)
	}
}

func TestWorkspaceWorkbenchImportRemapsConflicts(t *testing.T) {
	current := WorkCatalog{
		Modes:    []WorkMode{{ID: "review", Name: "当前模式", Permission: "workspace", Approval: "request"}},
		Commands: []QuickCommand{{ID: "check", Name: "当前指令", Content: "检查"}},
	}
	imported := workspaceArchiveWorkbench{
		Modes:    []WorkMode{{ID: "review", Name: "导入模式", Permission: "workspace", Approval: "request", Prompt: "不要读取密码"}},
		Commands: []QuickCommand{{ID: "check", Name: "导入指令", Content: "运行检查"}},
	}
	merged, mapping, err := mergeWorkspaceWorkbench(current, imported)
	if err != nil {
		t.Fatal(err)
	}
	if mapping["review"] == "review" || mapping["review"] == "" {
		t.Fatalf("expected imported mode ID remap, got %q", mapping["review"])
	}
	if len(merged.Modes) != 2 || len(merged.Commands) != 2 {
		t.Fatalf("unexpected merged catalog: %#v", merged)
	}
}

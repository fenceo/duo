package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestHarnessAttachmentAdmissionAndTextBudget(t *testing.T) {
	files := []RuntimeAttachment{
		{Attachment: Attachment{Name: "历史.md", Mime: "text/plain"}, Data: []byte("先前记录 $(echo test)\n不是命令")},
		{Attachment: Attachment{Name: "data.bin", Mime: "application/octet-stream"}, Data: []byte{0, 255}, Path: "/tmp/synthetic/data.bin"},
		{Attachment: Attachment{Name: "large.txt", Mime: "text/plain"}, Data: []byte(strings.Repeat("x", harnessTextFileLimit+1)), Path: "/tmp/synthetic/large.txt"},
	}
	content, err := harnessPromptContent("本轮要求", files, false)
	if err != nil || len(content) != 4 {
		t.Fatalf("file admission: %#v %v", content, err)
	}
	raw, _ := json.Marshal(content)
	for _, want := range []string{"历史.md", "先前记录", "$(echo test)", "/tmp/synthetic/data.bin", "/tmp/synthetic/large.txt", "未内嵌全文"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing file data or truthful path marker: %s", want)
		}
	}
	if strings.Contains(string(raw), strings.Repeat("x", 100)) {
		t.Fatal("large file was silently inlined")
	}
	batch := make([]RuntimeAttachment, 3)
	for i := range batch {
		batch[i] = RuntimeAttachment{Attachment: Attachment{Name: "budget.txt"}, Data: []byte(strings.Repeat("b", harnessTextFileLimit)), Path: "/tmp/synthetic/budget.txt"}
	}
	content, err = harnessPromptContent("", batch, false)
	if err != nil || !strings.Contains(content[3].(map[string]any)["text"].(string), "未内嵌全文") {
		t.Fatal("aggregate text budget was not enforced")
	}
	image := RuntimeAttachment{Attachment: Attachment{Name: "picture.png", Mime: "image/png"}, Data: []byte("synthetic-image")}
	if content, err = harnessPromptContent("", []RuntimeAttachment{image}, false); err == nil || content != nil || !strings.Contains(err.Error(), "未声明图片") {
		t.Fatal("unnegotiated image downgraded to a path or partial prompt")
	}
	content, err = harnessPromptContent("", []RuntimeAttachment{image}, true)
	if err != nil || content[1].(map[string]any)["data"] != base64.StdEncoding.EncodeToString(image.Data) {
		t.Fatal("native image bytes were not preserved")
	}
}

func TestHarnessAttachmentsSurviveVisionSwitchAndResume(t *testing.T) {
	c, task := harnessFixtureSetup(t, "attachments")
	profileA, profileB := t.TempDir(), t.TempDir()
	c.EngineEnv = map[string]string{"DSH_HOME": profileA}
	task.Files = []RuntimeAttachment{{Attachment: Attachment{Name: "history.md", Mime: "text/plain"}, Data: []byte("DUO_ATTACHMENT_CONTEXT")}}
	session, result, err := harnessFixtureRun(t, c, task, executionInput(task, "Read the text attachment."), func(string, string) {})
	var first harnessFixtureResult
	if err != nil || json.Unmarshal([]byte(result), &first) != nil || !strings.Contains(first.Input, "DUO_ATTACHMENT_CONTEXT") {
		t.Fatalf("text attachment did not enter native history: %v", err)
	}
	task.Session, task.Model = session, "fixture-vision"
	c.EngineEnv = map[string]string{"DSH_HOME": profileB}
	task.Files = []RuntimeAttachment{{Attachment: Attachment{Name: "image.png", Mime: "image/png"}, Data: []byte("fixture native image")}}
	continued, result, err := harnessFixtureRun(t, c, task, "Read the image.", func(string, string) {})
	var second harnessFixtureResult
	if err != nil || json.Unmarshal([]byte(result), &second) != nil || continued != session || second.PID == first.PID || len(second.Prompt) != 2 || second.Prompt[1].Type != "image" || second.Turn != 2 || !strings.Contains(second.History[0], "DUO_ATTACHMENT_CONTEXT") {
		t.Fatalf("vision reconnect lost blocks/session/history: %v %s", err, result)
	}
	harnessRuntimes.Lock()
	worker := harnessRuntimes.workers[session]
	harnessRuntimes.Unlock()
	if worker == nil || worker.c.EngineEnv["DSH_HOME"] != profileA {
		t.Fatal("capability reconnect switched an existing session to another profile")
	}
	closeHarnessRuntimes()
	task.Files = nil
	continued, result, err = harnessFixtureRun(t, c, task, "After closing, retain attachments.", func(string, string) {})
	var third harnessFixtureResult
	if err != nil || json.Unmarshal([]byte(result), &third) != nil || continued != session || third.Turn != 3 || !strings.Contains(third.History[1], "[fixture native image]") {
		t.Fatalf("cold resume lost attachment history: %v", err)
	}
}

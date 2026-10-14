package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTaskForkOver64MiBStagesCompleteUTF8History(t *testing.T) {
	runner := &switchRunner{}
	a, source := switchFixture(t, runner)
	if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created,finished) VALUES('large-history',?,'first request api_key=synthetic-secret','chat','web','done','last reply',1,2)", source.ID); err != nil {
		t.Fatal(err)
	}
	const chunks = 33
	const chars = (2 << 20) / 3
	payload := strings.Repeat("界", chars)
	for i := 0; i < chunks; i++ {
		if err := a.store.event(source.ID, "large-history", "tool", fmt.Sprintf("chunk-%02d\n%s\n", i, payload)); err != nil {
			t.Fatal(err)
		}
	}
	// Reproduce the old bounded archive failure, then exercise the real fork.
	if _, _, err := a.buildContinuation(source, "full"); !errors.Is(err, errContinuationArchiveTooLarge) {
		t.Fatal("fixture did not exceed the original archive limit", err)
	}
	branch, err := a.forkTask(source.ID)
	if err != nil {
		t.Fatal("large history could not fork", err)
	}
	var archived string
	if err = a.store.QueryRow("SELECT archive FROM task_handoff_context WHERE id=? AND task_id=?", branch.Binding.HistoryID, branch.ID).Scan(&archived); err != nil {
		t.Fatal(err)
	}
	var manifest continuationPartsManifest
	if !strings.HasPrefix(archived, continuationPartsPrefix) || json.Unmarshal([]byte(strings.TrimPrefix(archived, continuationPartsPrefix)), &manifest) != nil || manifest.Bytes <= continuationArchiveLimit {
		t.Fatal("fork did not retain a complete large archive manifest")
	}
	if _, err = a.store.Exec("UPDATE runs SET input='parent changed later' WHERE id='large-history'"); err != nil {
		t.Fatal(err)
	}
	brief, files, cleanup, err := a.stageContinuation(t.Context(), branch)
	if err != nil {
		t.Fatal("large fork could not prepare its next turn", err)
	}
	defer cleanup()
	if len(files) != manifest.Parts+1 || files[0].Name != "Duo-历史对话.md" || !strings.Contains(brief, "分为") {
		t.Fatal("history index or volumes were not provided")
	}
	index, err := os.ReadFile(files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	bytes, characters := 0, 0
	markers := make(map[int]bool)
	tail := ""
	for _, file := range files[1:] {
		data, err := os.ReadFile(file.Path)
		if err != nil || len(data) > continuationPartBytes || int64(len(data)) != file.Size || !utf8.Valid(data) || len(file.Data) != 0 {
			t.Fatal("volume exceeded its budget, split UTF-8 or retained all data", err)
		}
		if !strings.Contains(string(index), file.Name) || !strings.Contains(string(index), filepath.Base(file.Path)) {
			t.Fatal("index omitted a volume")
		}
		text := string(data)
		if strings.Contains(text, "synthetic-secret") || strings.Contains(text, "parent changed later") {
			t.Fatal("archive leaked credentials or included a later parent edit")
		}
		for i := 0; i < chunks; i++ {
			if strings.Contains(tail+text, fmt.Sprintf("chunk-%02d", i)) {
				markers[i] = true
			}
		}
		tail = text[max(0, len(text)-128):]
		bytes += len(data)
		characters += strings.Count(text, "界")
		hash.Write(data)
	}
	if bytes != manifest.Bytes || hex.EncodeToString(hash.Sum(nil)) != manifest.SHA256 || characters != chunks*chars || len(markers) != chunks {
		t.Fatal("early/late records or bytes were lost while splitting history")
	}
	current, err := a.store.task(source.ID)
	if err != nil || current.Session != source.Session || len(runner.calls) != 0 {
		t.Fatal("fork changed the parent's native session or called a model", err)
	}
	cleanup()
	for _, file := range files {
		if _, err := os.Stat(file.Path); !os.IsNotExist(err) {
			t.Fatal("temporary history file survived cleanup", err)
		}
	}
}

func TestTaskForkPartsRollbackAndIncompleteStageCleanup(t *testing.T) {
	a, source := switchFixture(t, &switchRunner{})
	if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created,finished) VALUES('rollback-history',?,'first request','chat','web','done','last reply',1,2)", source.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.store.event(source.ID, "rollback-history", "tool", strings.Repeat("x", continuationPartBytes+100)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Exec("CREATE TRIGGER fail_fork_part BEFORE INSERT ON task_handoff_archive_parts BEGIN SELECT RAISE(ABORT,'synthetic part write failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.forkTask(source.ID); err == nil {
		t.Fatal("archive write failure did not abort the fork")
	}
	for table, want := range map[string]int{"tasks": 1, "runs": 1, "task_handoff_context": 0, "task_handoff_archive_parts": 0} {
		var count int
		if err := a.store.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != want {
			t.Fatal("failed fork left partial rows", table, count, err)
		}
	}
	if _, err := a.store.Exec("DROP TRIGGER fail_fork_part"); err != nil {
		t.Fatal(err)
	}
	branch, err := a.forkTask(source.ID)
	if err != nil {
		t.Fatal(err)
	}
	var lastPart string
	if err = a.store.QueryRow("SELECT archive FROM task_handoff_archive_parts WHERE history_id=? AND part=1", branch.Binding.HistoryID).Scan(&lastPart); err != nil {
		t.Fatal(err)
	}
	if _, err = a.store.Exec("DELETE FROM task_handoff_archive_parts WHERE history_id=? AND part=1", branch.Binding.HistoryID); err != nil {
		t.Fatal(err)
	}
	_, files, cleanup, err := a.stageContinuation(t.Context(), branch)
	cleanup()
	if err == nil || len(files) != 0 {
		t.Fatal("incomplete history was handed to the runner")
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(a.config.path), "attachments"))
	if err != nil || len(entries) != 0 {
		t.Fatal("failed staging left previously prepared parts", err)
	}
	// Same byte count and valid UTF-8 still require the full-history digest.
	if _, err = a.store.Exec("INSERT INTO task_handoff_archive_parts VALUES(?,1,?); UPDATE task_handoff_archive_parts SET archive='!'||substr(archive,2) WHERE history_id=? AND part=0", branch.Binding.HistoryID, lastPart, branch.Binding.HistoryID); err != nil {
		t.Fatal(err)
	}
	var bytes int
	if err = a.store.QueryRow("SELECT length(CAST(archive AS BLOB)) FROM task_handoff_archive_parts WHERE history_id=? AND part=0", branch.Binding.HistoryID).Scan(&bytes); err != nil || bytes != continuationPartBytes {
		t.Fatal("corruption fixture changed the volume byte count", bytes, err)
	}
	_, files, cleanup, err = a.stageContinuation(t.Context(), branch)
	cleanup()
	if err == nil || len(files) != 0 {
		t.Fatal("changed history bytes passed the archive digest")
	}
	entries, err = os.ReadDir(filepath.Join(filepath.Dir(a.config.path), "attachments"))
	if err != nil || len(entries) != 0 {
		t.Fatal("digest failure left prepared history files", err)
	}
}

package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

func insertWorkspacePagingRuns(t *testing.T, a *App, task Task, count int, result string, offset int) {
	t.Helper()
	tx, err := a.store.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("page-%06d", offset+i)
		if _, err = tx.Exec("INSERT INTO runs(id,task_id,input,result,kind,source,status,created,finished) VALUES(?,?,?,?,'chat','web','done',?,?)", id, task.ID, "synthetic requirement", result, offset+i+10, offset+i+11); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func drainWorkspacePagingFixture(t *testing.T, a *App, task Task, previous WorkspaceMemoryReport) (map[string]bool, WorkspaceMemoryReport, int) {
	t.Helper()
	seen := map[string]bool{}
	for batch := 0; batch < 100; batch++ {
		runs, _, checkpoint, err := a.store.workspaceMemoryRunPage(context.Background(), task, previous)
		if err != nil {
			t.Fatal(err)
		}
		for _, run := range runs {
			seen[run.ID] = true
		}
		previous.Tasks = []WorkspaceMemoryTask{{ID: task.ID, SyncCursor: checkpoint.Cursor, SyncCursorRunID: checkpoint.CursorRunID, SyncPendingRows: checkpoint.PendingRows, SyncPending: checkpoint.Pending}}
		if !checkpoint.Pending {
			return seen, previous, batch + 1
		}
	}
	t.Fatal("historical scan did not drain")
	return nil, previous, 0
}

func TestWorkspaceMemoryPagingBeyondTwoThousandAndLateRunningHole(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,result,kind,source,status,created) VALUES('old-running',?,'pending','','chat','web','running',1)", task.ID); err != nil {
		t.Fatal(err)
	}
	insertWorkspacePagingRuns(t, a, task, 2107, "synthetic completed result", 0)
	seen, previous, batches := drainWorkspacePagingFixture(t, a, task, WorkspaceMemoryReport{})
	if len(seen) != 2107 || batches < 2 || len(previous.Tasks[0].SyncPendingRows) != 1 {
		t.Fatal("old history or active hole lost", len(seen), batches, previous.Tasks)
	}
	if _, err := a.store.Exec("UPDATE runs SET status='done',result='late result',finished=99999 WHERE id='old-running'"); err != nil {
		t.Fatal(err)
	}
	// More than six newer rounds complete before the next scan. The old hole
	// must still export, independent of the recent source excerpt window.
	insertWorkspacePagingRuns(t, a, task, 80, "newer completed result", 3000)
	seen, previous, _ = drainWorkspacePagingFixture(t, a, task, previous)
	if !seen["old-running"] || len(previous.Tasks[0].SyncPendingRows) != 0 {
		t.Fatal("late completed older run stranded behind recent tail", previous.Tasks)
	}
}

func TestWorkspaceMemoryPagingRawHistoryBeyondThirtyTwoMiB(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	result := strings.Repeat("synthetic-long-result ", 7000)
	insertWorkspacePagingRuns(t, a, task, 260, result, 0)
	seen, _, batches := drainWorkspacePagingFixture(t, a, task, WorkspaceMemoryReport{})
	if len(seen) != 260 || batches < 4 {
		t.Fatal("raw byte budget permanently excluded older history", len(seen), batches)
	}
}

func TestWorkspaceMemoryPagingReusedRowIDResetsReceipt(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	insertWorkspacePagingRuns(t, a, task, 8, "synthetic result", 0)
	_, previous, _ := drainWorkspacePagingFixture(t, a, task, WorkspaceMemoryReport{})
	if _, err := a.store.Exec("DELETE FROM runs WHERE id='page-000007'"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.Exec("INSERT INTO runs(id,task_id,input,result,kind,source,status,created,finished) VALUES('imported-old-run',?,'import','imported result','chat','web','done',1,2)", task.ID); err != nil {
		t.Fatal(err)
	}
	_, _, checkpoint, err := a.store.workspaceMemoryRunPage(context.Background(), task, previous)
	if err != nil || !checkpoint.LogIDs["imported-old-run"] {
		t.Fatal("reused rowid was stranded outside history and recent tail", checkpoint, err)
	}
}

func TestWorkspaceWorklogRolloverPreservesManualBytesAndEverySource(t *testing.T) {
	_, task := workspaceMemoryFixture(t)
	base := workspaceMemoryTaskBase(task)
	main := path.Join(base, workspaceWorklogFilename)
	manual := "# human task notes\n" + strings.Repeat("manual content stays unchanged\n", 10000)
	filename := filepath.Join(task.Workspace, filepath.FromSlash(main))
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(manual), 0600); err != nil {
		t.Fatal(err)
	}
	entries := []workspaceWorklogEntry{}
	for i := 0; i < 6; i++ {
		entries = append(entries, workspaceWorklogEntry{ID: fmt.Sprintf("%s/overflow-%d", task.ID, i), Content: fmt.Sprintf("### 2026-10-%02d\n\n", i+1) + strings.Repeat("synthetic evidence\n", 5000), Details: true, Month: "2026-10"})
	}
	payload := []workspaceMemoryFile{{Path: main, Content: "unused", AppendWorklog: true, Entries: entries}}
	report, err := workspaceMemoryIO(context.Background(), task, "sync", payload)
	if err != nil {
		t.Fatal(report, err)
	}
	current := readWorkspaceWorklog(t, task, main)
	if !strings.HasPrefix(current, manual) || !strings.Contains(current, "history/README.md") || len(current) > workspaceMemoryFileLimit {
		t.Fatal("manual main was truncated, navigation missing or unbounded", len(current))
	}
	pages, err := filepath.Glob(filepath.Join(task.Workspace, filepath.FromSlash(base), "history", "2026-10-*.md"))
	if err != nil || len(pages) < 2 {
		t.Fatal("history failed to roll over", pages, err)
	}
	all := ""
	for _, filename := range pages {
		raw, e := os.ReadFile(filename)
		if e != nil || len(raw) > workspaceMemoryFileLimit {
			t.Fatal(filename, e)
		}
		all += string(raw)
	}
	for _, entry := range entries {
		if strings.Count(all, workspaceWorklogMarker(entry.ID)) != 1 {
			t.Fatal("paged source missing or duplicated", entry.ID)
		}
	}
	// Edited historical pages are still append-only, and a repeated sync cannot
	// duplicate prior entries even after CRLF editor conversion.
	first, err := os.ReadFile(pages[0])
	if err != nil {
		t.Fatal(err)
	}
	humanPage := strings.ReplaceAll(string(first), "\n", "\r\n") + "\r\nhuman page annotation\r\n"
	if err = os.WriteFile(pages[0], []byte(humanPage), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = workspaceMemoryIO(context.Background(), task, "sync", payload); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(pages[0]); string(raw) != humanPage {
		t.Fatal("edited page was rewritten")
	}
	if readWorkspaceWorklog(t, task, main) != current {
		t.Fatal("repeat paged sync changed primary document")
	}
	for _, relative := range []string{path.Join(base, "history", "README.md"), path.Join(base, "history", "2026-10.md"), path.Join(base, "history", "2026-10-0001.md")} {
		if !workspaceMemoryDocumentPath(relative) {
			t.Fatal("history document cannot open in Duo", relative)
		}
	}
	for _, relative := range []string{path.Join(base, "history", "../other.md"), base + "/history/2026-13-0001.md", base + "/history/../../outside.md"} {
		if workspaceMemoryDocumentPath(relative) {
			t.Fatal("unsafe history path accepted", relative)
		}
	}
}

func TestWorkspaceWorklogNavigationCannotNestInsideState(t *testing.T) {
	content := workspaceWorklogHeader(Task{ID: "navigation-task", Title: "synthetic task"}, "")
	updated, conflict := updateWorkspaceWorklogNavigation(content, "## Recent history\n\nlatest fixture")
	if conflict || strings.Index(updated, workspaceWorklogNavigationStart) < strings.Index(updated, workspaceTaskStateEnd) {
		t.Fatal("navigation was inserted inside task state", updated)
	}
	stateUpdated, stateConflict := updateWorkspaceWorklogState(updated, "## Current state\n\nupdated valid state")
	if stateConflict || !strings.Contains(stateUpdated, "updated valid state") {
		t.Fatal("navigation invalidated task-state managed hash", stateUpdated)
	}
	navigationUpdated, navigationConflict := updateWorkspaceWorklogNavigation(stateUpdated, "## Recent history\n\nnew preview")
	if navigationConflict || !strings.Contains(navigationUpdated, "new preview") || strings.Count(navigationUpdated, workspaceTaskStateStart) != 1 {
		t.Fatal("task-state update invalidated separate navigation block", navigationUpdated)
	}
}

func TestWorkspaceMemoryCheckpointWrittenOnlyAfterWorklogSuccess(t *testing.T) {
	a, task := workspaceMemoryFixture(t)
	workspaceMemoryRun(t, a, task, "first requirement", "first result", "done")
	report, err := a.syncWorkspaceMemory(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	handoff := path.Join(report.TaskDirectory, "handoff.md")
	before := readWorkspaceWorklog(t, task, handoff)
	workspaceMemoryRun(t, a, task, "second requirement", "second result", "done")
	files, _, err := a.store.workspaceMemoryFiles(context.Background(), task)
	if err != nil {
		t.Fatal(err)
	}
	for i := range files {
		if files[i].AppendWorklog {
			files[i].Entries = append(files[i].Entries, workspaceWorklogEntry{ID: "invalid/../source", Content: "must fail"})
		}
	}
	if _, err = workspaceMemoryIO(context.Background(), task, "sync", files); err == nil {
		t.Fatal("invalid worklog append unexpectedly succeeded")
	}
	if readWorkspaceWorklog(t, task, handoff) != before {
		t.Fatal("failed worklog advanced durable checkpoint")
	}
	if _, err = a.syncWorkspaceMemory(context.Background(), task.ID); err != nil {
		t.Fatal("safe retry did not recover", err)
	}
}

func TestWorkspaceWorklogPagingDedupeAcrossMonthsAndOversizeManualFiles(t *testing.T) {
	_, task := workspaceMemoryFixture(t)
	base := workspaceMemoryTaskBase(task)
	main := path.Join(base, workspaceWorklogFilename)
	filename := filepath.Join(task.Workspace, filepath.FromSlash(main))
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	identity := task.ID + "/existing-large-main"
	manual := "# manual oversized log\n" + workspaceWorklogMarker(identity) + "\n" + workspaceWorklogDetailMarker(identity) + "\n" + strings.Repeat("x", workspaceMemoryFileLimit)
	if err := os.WriteFile(filename, []byte(manual), 0600); err != nil {
		t.Fatal(err)
	}
	origin := workspaceMemoryTaskBase(Task{ID: "original-task", Title: "Original task"})
	source := path.Join(origin, "runs", "original-run.md")
	entry := workspaceWorklogEntry{ID: "original-task/original-run", Content: "### chronological source\n\n[来源记录](" + workspaceMemoryLink(main, source) + ")\n\nEvidence of completed synthetic fixture.", Details: true, Source: source, Month: "2026-10"}
	entries := []workspaceWorklogEntry{{ID: identity, Content: "must not duplicate existing main source", Details: true, Month: "2026-10"}, entry}
	if _, err := workspaceMemoryIO(context.Background(), task, "sync", []workspaceMemoryFile{{Path: main, AppendWorklog: true, Entries: entries}}); err != nil {
		t.Fatal(err)
	}
	if readWorkspaceWorklog(t, task, main) != manual {
		t.Fatal("oversize manual main rewritten")
	}
	page := path.Join(base, "history", "2026-10-0001.md")
	pageText := readWorkspaceWorklog(t, task, page)
	if strings.Contains(pageText, workspaceWorklogMarker(identity)) || !strings.Contains(pageText, "]("+workspaceMemoryLink(page, source)+")") {
		t.Fatal("oversize dedupe or fork source rebasing failed", pageText)
	}
	// A corrected timestamp must not create the same source in another month.
	entry.Month = "2026-11"
	if _, err := workspaceMemoryIO(context.Background(), task, "sync", []workspaceMemoryFile{{Path: main, AppendWorklog: true, Entries: []workspaceWorklogEntry{entry}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(task.Workspace, filepath.FromSlash(path.Join(base, "history", "2026-11-0001.md")))); !os.IsNotExist(err) {
		t.Fatal("source duplicated into changed month", err)
	}
	// Manual expansion of an existing page must not stall later exports or lose
	// its marker receipts; new content moves to the next numbered page.
	oversizePage := pageText + strings.Repeat("human addition\n", 100000)
	if err := os.WriteFile(filepath.Join(task.Workspace, filepath.FromSlash(page)), []byte(oversizePage), 0600); err != nil {
		t.Fatal(err)
	}
	next := workspaceWorklogEntry{ID: task.ID + "/later-source", Content: "later evidence", Month: "2026-10", Details: true}
	if _, err := workspaceMemoryIO(context.Background(), task, "sync", []workspaceMemoryFile{{Path: main, AppendWorklog: true, Entries: []workspaceWorklogEntry{entry, next}}}); err != nil {
		t.Fatal(err)
	}
	if readWorkspaceWorklog(t, task, page) != oversizePage {
		t.Fatal("oversize historical page rewritten")
	}
	newPage := readWorkspaceWorklog(t, task, path.Join(base, "history", "2026-10-0002.md"))
	if !strings.Contains(newPage, workspaceWorklogMarker(next.ID)) || strings.Contains(newPage, workspaceWorklogMarker(entry.ID)) {
		t.Fatal("oversize page stalled or duplicated history", newPage)
	}
}

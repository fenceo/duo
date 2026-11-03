package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStickyBoardSaveConflictAndIsolation(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	request := toolsClient(t, a)
	var board StickyBoard
	json.Unmarshal(request("/api/sticky", "GET", nil, 200), &board)
	if board.Revision != 0 || board.Color != "neutral" || board.Items == nil {
		t.Fatal(board)
	}
	board.Items = []StickyNote{{ID: "quick-1", Content: "随手记录\n第二行"}}
	json.Unmarshal(request("/api/sticky", "PUT", board, 200), &board)
	if board.Revision != 1 {
		t.Fatal(board)
	}
	stale := board
	board.Items[0].Done = true
	board.Color = "blue"
	json.Unmarshal(request("/api/sticky", "PUT", board, 200), &board)
	request("/api/sticky", "PUT", stale, 409)
	stored, _, err := readStickyBoard(a.store)
	if err != nil || stored.Revision != 2 || !stored.Items[0].Done || stored.Color != "blue" {
		t.Fatal(stored, err)
	}
	// A task-bound todo remains separate from the global board.
	task := taskFor(t, a)
	request("/api/tasks/"+task.ID+"/scratch", "POST", Scratch{Content: "旧任务待办"}, 201)
	if strings.Contains(string(request("/api/sticky", "GET", nil, 200)), "旧任务待办") {
		t.Fatal("task todo leaked into quick notes")
	}
	board.Items = nil
	request("/api/sticky", "PUT", board, 200)
	if !strings.Contains(string(request("/api/tasks/"+task.ID+"/scratch", "GET", nil, 200)), "旧任务待办") {
		t.Fatal("task todo was deleted")
	}
	// Authentication and CSRF are required for the new route as well.
	h := (&Server{app: a}).Handler()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/sticky", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	r := httptest.NewRequest("PUT", "/api/sticky", strings.NewReader(`{}`))
	r.Header.Set("Cookie", "jianzuo_session=tools-test")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestStickyValidationAndArchive(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	request := toolsClient(t, a)
	base := StickyBoard{Color: "purple", Items: []StickyNote{{ID: "same", Content: "记住"}}}
	for _, bad := range []StickyBoard{
		{Color: "injection"}, {Color: "neutral", Revision: -1},
		{Color: "neutral", Items: []StickyNote{{ID: "x", Content: "one", Color: "injection"}}},
		{Color: "neutral", Items: []StickyNote{{ID: "x", Content: " "}}},
		{Color: "neutral", Items: []StickyNote{{ID: "x", Content: strings.Repeat("a", 24001)}}},
		{Color: "neutral", Items: []StickyNote{{ID: "x", Content: "one"}, {ID: "x", Content: "two"}}},
	} {
		request("/api/sticky", "PUT", bad, 400)
	}
	base.Items[0].Content = "备忘 password=hunter2"
	base.Items[0].Pinned = true
	base.Items[0].Color = "green"
	request("/api/sticky", "PUT", base, 200)
	archive, files, err := a.store.workspaceArchiveSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if archive.Sticky == nil || len(archive.Sticky.Items) != 1 || strings.Contains(archive.Sticky.Items[0].Content, "hunter2") {
		t.Fatal("export must preserve notes and redact secrets", archive.Sticky)
	}
	zip, err := buildWorkspaceZip(archive, files)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := readWorkspaceZip(zip)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := a.store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = importStickyBoard(tx, decoded.Archive.Sticky); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	board, _, err := readStickyBoard(a.store)
	if err != nil || board.Revision != 2 || len(board.Items) != 2 || board.Items[0].ID == board.Items[1].ID {
		t.Fatal("import must append, regenerate IDs and advance revision", board, err)
	}
	if board.Items[0].Content != base.Items[0].Content {
		t.Fatal("export/import mutated the original note")
	}
	for _, note := range board.Items {
		if !note.Pinned || note.Color != "green" {
			t.Fatal("export/import lost per-note pin or color", note)
		}
	}
}

func TestStickyPerNoteAppearanceAndLegacyRead(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	request := toolsClient(t, a)
	legacy := `{"revision":7,"color":"purple","items":[{"id":"first","content":"first","done":false},{"id":"second","content":"second","done":true}]}`
	if _, err := a.store.Exec("INSERT INTO settings(key,value) VALUES('sticky_board',?)", legacy); err != nil {
		t.Fatal(err)
	}
	var board StickyBoard
	if err := json.Unmarshal(request("/api/sticky", "GET", nil, 200), &board); err != nil {
		t.Fatal(err)
	}
	for _, note := range board.Items {
		if note.Color != "purple" || note.Pinned {
			t.Fatal("legacy notes must keep their old color without being pinned", board)
		}
	}
	var raw string
	if err := a.store.QueryRow("SELECT value FROM settings WHERE key='sticky_board'").Scan(&raw); err != nil || raw != legacy {
		t.Fatal("reading legacy notes must not rewrite storage or advance the revision", raw, err)
	}
	board.Items[0].Color = "yellow"
	board.Items[0].Pinned = true
	request("/api/sticky", "PUT", board, 200)
	if err := json.Unmarshal(request("/api/sticky", "GET", nil, 200), &board); err != nil {
		t.Fatal(err)
	}
	if board.Revision != 8 || board.Items[0].ID != "first" || !board.Items[0].Pinned || board.Items[0].Color != "yellow" || board.Items[1].ID != "second" || board.Items[1].Pinned || board.Items[1].Color != "purple" || !board.Items[1].Done {
		t.Fatal("saving one note must preserve other note appearance, content and order", board)
	}
	board.Items[0].Pinned = false
	request("/api/sticky", "PUT", board, 200)
	stored, _, err := readStickyBoard(a.store)
	if err != nil || stored.Items[0].Pinned || stored.Items[0].Color != "yellow" || stored.Items[1].Color != "purple" {
		t.Fatal("unpinning must persist independently from color", stored, err)
	}
}

func TestStickyImportLegacyColorIsolation(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	request := toolsClient(t, a)
	request("/api/sticky", "PUT", StickyBoard{Color: "neutral", Items: []StickyNote{{ID: "local", Content: "local", Color: "blue", Pinned: true}}}, 200)
	incoming := StickyBoard{Color: "yellow", Items: []StickyNote{{ID: "old", Content: "old"}, {ID: "custom", Content: "custom", Color: "green", Pinned: true}}}
	tx, err := a.store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := importStickyBoard(tx, &incoming); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	board, _, err := readStickyBoard(a.store)
	if err != nil || len(board.Items) != 3 {
		t.Fatal(board, err)
	}
	if board.Color != "neutral" || board.Items[0].Color != "blue" || !board.Items[0].Pinned || board.Items[1].Color != "yellow" || board.Items[1].Pinned || board.Items[2].Color != "green" || !board.Items[2].Pinned {
		t.Fatal("imported board color must only apply to its own legacy notes", board)
	}
	if incoming.Items[0].ID != "old" || incoming.Items[0].Color != "" {
		t.Fatal("import must not mutate the source archive", incoming)
	}
}

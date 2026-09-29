package main

// Quick notes belong to the workspace, independent of a task or model session.
// One revision protects the ordered board against lost updates across windows.
import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type StickyNote struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Done    bool   `json:"done"`
}

type StickyBoard struct {
	Revision int64        `json:"revision"`
	Color    string       `json:"color"`
	Items    []StickyNote `json:"items"`
}

var errStickyConflict = errors.New("便签已在其他窗口修改，当前草稿已保留，请核对后重新加载")

func validateStickyBoard(b StickyBoard) error {
	if b.Revision < 0 || b.Revision >= 1<<52 || len(b.Items) > 500 {
		return errors.New("便签数量或版本无效，最多 500 条")
	}
	switch b.Color {
	case "neutral", "yellow", "blue", "green", "purple":
	default:
		return errors.New("便签颜色无效")
	}
	seen, size := map[string]bool{}, 0
	for _, n := range b.Items {
		size += len(n.Content)
		if !safeWorkbenchID(n.ID) || seen[n.ID] || strings.TrimSpace(n.Content) == "" || len(n.Content) > 24000 || strings.ContainsRune(n.Content, 0) {
			return errors.New("便签内容或标识无效，每条最多 24 KiB")
		}
		seen[n.ID] = true
	}
	if size > 512<<10 {
		return errors.New("便签总内容最多 512 KiB")
	}
	return nil
}

func readStickyBoard(q interface{ QueryRow(string, ...any) *sql.Row }) (StickyBoard, string, error) {
	b := StickyBoard{Color: "neutral", Items: []StickyNote{}}
	var raw string
	err := q.QueryRow("SELECT value FROM settings WHERE key='sticky_board'").Scan(&raw)
	if err == sql.ErrNoRows {
		return b, "", nil
	}
	if err != nil {
		return b, "", err
	}
	if err = json.Unmarshal([]byte(raw), &b); err == nil {
		err = validateStickyBoard(b)
	}
	return b, raw, err
}

func writeStickyBoard(tx *sql.Tx, b StickyBoard, previous string) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	var result sql.Result
	if previous == "" {
		result, err = tx.Exec("INSERT INTO settings(key,value) VALUES('sticky_board',?) ON CONFLICT(key) DO NOTHING", string(raw))
	} else {
		result, err = tx.Exec("UPDATE settings SET value=? WHERE key='sticky_board' AND value=?", string(raw), previous)
	}
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return errStickyConflict
	}
	return nil
}

func (s *Server) stickyRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/sticky", s.secure(func(w http.ResponseWriter, r *http.Request) {
		b, _, err := readStickyBoard(s.app.store)
		if err != nil {
			fail(w, 500, "无法读取便签，请重试")
			return
		}
		jsonOut(w, 200, b)
	}))
	m.HandleFunc("PUT /api/sticky", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var b StickyBoard
		if !body(w, r, &b) {
			return
		}
		if err := validateStickyBoard(b); err != nil {
			fail(w, 400, err.Error())
			return
		}
		tx, err := s.app.store.Begin()
		if err != nil {
			fail(w, 500, "无法保存便签")
			return
		}
		defer tx.Rollback()
		current, raw, err := readStickyBoard(tx)
		if err != nil {
			fail(w, 500, "无法读取便签版本")
			return
		}
		if current.Revision != b.Revision {
			fail(w, 409, errStickyConflict.Error())
			return
		}
		b.Revision++
		if b.Items == nil {
			b.Items = []StickyNote{}
		}
		if err = writeStickyBoard(tx, b, raw); err != nil {
			fail(w, 409, errStickyConflict.Error())
			return
		}
		if err = tx.Commit(); err != nil {
			fail(w, 500, "无法保存便签，请重试")
			return
		}
		jsonOut(w, 200, b)
	}))
}

// Imports are additive, just like tasks and commands. Preserve local color and
// regenerate imported IDs; bump the revision so open editors detect the change.
func importStickyBoard(tx *sql.Tx, incoming *StickyBoard) error {
	if incoming == nil || len(incoming.Items) == 0 {
		return nil
	}
	current, raw, err := readStickyBoard(tx)
	if err != nil {
		return err
	}
	if len(current.Items) == 0 {
		current.Color = incoming.Color
	}
	for _, n := range incoming.Items {
		n.ID = uid()
		current.Items = append(current.Items, n)
	}
	current.Revision++
	if err = validateStickyBoard(current); err != nil {
		return err
	}
	return writeStickyBoard(tx, current, raw)
}

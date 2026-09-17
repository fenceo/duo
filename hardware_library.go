package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
)

type HardwareTaskUse struct {
	ID       string         `json:"id"`
	Title    string         `json:"title"`
	Archived bool           `json:"archived"`
	AI       HardwareGrant  `json:"ai"`
	ActiveAI *HardwareGrant `json:"active_ai,omitempty"`
}
type HardwareOverview struct {
	Config          HardwareConfig    `json:"config"`
	Connected       bool              `json:"connected"`
	ControllerTask  string            `json:"controller_task"`
	ControllerTitle string            `json:"controller_title"`
	Tasks           []HardwareTaskUse `json:"tasks"`
}

func (s *Server) hardwareOverview(w http.ResponseWriter, r *http.Request) {
	rows, err := s.app.store.Query(`SELECT h.id,h.config,t.id,t.title,COALESCE(pref.archived,0),
		COALESCE(p.allow_read,0),COALESCE(p.allow_write,0),COALESCE(p.allow_power,0)
		FROM hardware h LEFT JOIN task_hardware b ON b.hardware_id=h.id
		LEFT JOIN tasks t ON t.id=b.task_id
		LEFT JOIN task_preferences pref ON pref.task_id=t.id
		LEFT JOIN hardware_ai_access p ON p.task_id=t.id AND p.hardware_id=h.id
		ORDER BY h.rowid DESC,t.created DESC,t.id`)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	out := []*HardwareOverview{}
	byID := map[string]*HardwareOverview{}
	active := s.app.hardwareAI.activeGrants()
	for rows.Next() {
		var id, raw string
		var task, title sql.NullString
		var archived sql.NullBool
		var g HardwareGrant
		if err = rows.Scan(&id, &raw, &task, &title, &archived, &g.Read, &g.Write, &g.Power); err != nil {
			break
		}
		item := byID[id]
		if item == nil {
			item = &HardwareOverview{Tasks: []HardwareTaskUse{}}
			if err = json.Unmarshal([]byte(raw), &item.Config); err != nil {
				break
			}
			byID[id] = item
			out = append(out, item)
		}
		if task.Valid {
			use := HardwareTaskUse{ID: task.String, Title: title.String, Archived: archived.Bool, AI: g}
			if original, ok := active[task.String][id]; ok && !use.Archived {
				effective := effectiveHardwareGrant(item.Config, g, original)
				use.ActiveAI = &effective
			}
			item.Tasks = append(item.Tasks, use)
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	for _, item := range out {
		item.Connected, _, item.ControllerTask, _ = s.app.hardware.connectionStatus(item.Config.ID)
		for _, task := range item.Tasks {
			if task.ID == item.ControllerTask {
				item.ControllerTitle = task.Title
			}
		}
	}
	jsonOut(w, 200, out)
}

func (h *Hardware) control(id, task, generation string, claim bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.links[id]
	if l == nil {
		return errors.New("设备尚未连接")
	}
	if generation == "" || generation != l.generation {
		return errors.New("控制权已变化，请刷新后重试")
	}
	if l.relay != nil && l.relay.isBusy() {
		return errors.New("电源操作进行中，完成后再切换控制权")
	}
	if !claim && l.controller != task {
		return errors.New("只有当前控制任务可以释放")
	}
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	previous := l.controller
	l.controller = ""
	if claim {
		l.controller = task
	}
	l.generation = uid()
	h.recordFor(id, "status", []byte("控制权变更："+previous+" → "+l.controller), task)
	return nil
}
func (h *Hardware) disconnectOwned(id, task, generation string) error {
	h.mu.Lock()
	l := h.links[id]
	if l == nil {
		h.mu.Unlock()
		return nil
	}
	if l.controller != task || (generation != "" && generation != l.generation) {
		h.mu.Unlock()
		return errors.New("请先取得控制权，再断开设备")
	}
	if l.relay != nil && l.relay.isBusy() {
		h.mu.Unlock()
		return errors.New("电源操作进行中，请等待完成")
	}
	l.writeMu.Lock()
	delete(h.links, id)
	owner := l.controller
	h.mu.Unlock()
	l.conn.Close()
	l.writeMu.Unlock()
	h.recordFor(id, "status", []byte("已断开"), owner)
	return nil
}
func (h *Hardware) detach(task, id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if l := h.links[id]; l != nil && l.controller == task {
		return errors.New("请先释放控制权或断开，再从任务移除")
	}
	_, err := h.store.Exec("DELETE FROM task_hardware WHERE task_id=? AND hardware_id=?", task, id)
	return err
}
func (s *Server) hardwareLibraryRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/hardware/overview", s.secure(s.hardwareOverview))
	m.HandleFunc("GET /api/hardware", s.secure(func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.app.store.Query("SELECT config FROM hardware ORDER BY rowid DESC")
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		out := []HardwareConfig{}
		for rows.Next() {
			var raw string
			if err = rows.Scan(&raw); err != nil {
				break
			}
			var c HardwareConfig
			if err = json.Unmarshal([]byte(raw), &c); err != nil {
				break
			}
			out = append(out, c)
		}
		rows.Close()
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		jsonOut(w, 200, out)
	}))
	m.HandleFunc("POST /api/tasks/{id}/hardware/{hid}/attach", s.secure(func(w http.ResponseWriter, r *http.Request) {
		task, err := s.app.store.task(r.PathValue("id"))
		if err != nil || task.Archived {
			fail(w, 409, "请先选择未归档任务")
			return
		}
		var raw string
		if s.app.store.QueryRow("SELECT config FROM hardware WHERE id=?", r.PathValue("hid")).Scan(&raw) != nil {
			fail(w, 404, "设备不存在")
			return
		}
		_, err = s.app.store.Exec("INSERT OR IGNORE INTO task_hardware VALUES(?,?)", task.ID, r.PathValue("hid"))
		if err != nil {
			fail(w, 500, err.Error())
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("POST /api/tasks/{id}/hardware/{hid}/relay", s.secure(s.relayAction))
}

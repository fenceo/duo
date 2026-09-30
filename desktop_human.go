package main

import "net/http"

// Browser control is deliberately separate from the AI action schema. A model
// cannot set a flag to ignore physical user input or mint a browser-control lease.
func (s *Server) desktopHumanRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/tasks/{id}/desktop/control", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Enabled bool   `json:"enabled"`
			Token   string `json:"token"`
		}
		if !body(w, r, &v) {
			return
		}
		task := r.PathValue("id")
		if !v.Enabled {
			if err := s.app.desktop.releaseHuman(task, v.Token); err != nil {
				fail(w, 409, err.Error())
				return
			}
			s.app.changed()
			jsonOut(w, 200, s.app.desktop.status())
			return
		}
		t, err := s.app.store.task(task)
		if err != nil || t.Archived || t.Deleted {
			fail(w, 400, "任务不可用")
			return
		}
		token, err := s.app.desktop.takeHuman(task)
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		s.app.changed()
		jsonOut(w, 200, map[string]any{"token": token, "state": s.app.desktop.status()})
	}))
	m.HandleFunc("POST /api/tasks/{id}/desktop/control/frame", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Token string `json:"token"`
		}
		if !body(w, r, &v) {
			return
		}
		if v.Token == "" {
			fail(w, 400, "缺少手动控制授权")
			return
		}
		frame, err := s.app.desktop.observeAs(r.Context(), r.PathValue("id"), "", v.Token)
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		jsonOut(w, 200, frame)
	}))
	m.HandleFunc("POST /api/tasks/{id}/desktop/control/action", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Token  string        `json:"token"`
			Action DesktopAction `json:"action"`
		}
		if !body(w, r, &v) {
			return
		}
		if v.Token == "" {
			fail(w, 400, "缺少手动控制授权")
			return
		}
		if err := s.app.desktop.actAs(r.Context(), r.PathValue("id"), "", v.Token, v.Action); err != nil {
			fail(w, 409, err.Error())
			return
		}
		jsonOut(w, 200, map[string]bool{"ok": true})
	}))
}

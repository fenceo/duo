package main

import (
	"net/http"
	"os"
)

func (s *Server) taskOpenCommand(w http.ResponseWriter, r *http.Request) {
	task, err := s.app.store.task(r.PathValue("id"))
	if err != nil || task.Deleted || !safeWorkbenchID(task.ID) {
		fail(w, http.StatusNotFound, "任务不存在或已删除")
		return
	}
	executable, err := os.Executable()
	if err != nil {
		fail(w, http.StatusInternalServerError, "无法确定当前程序路径")
		return
	}
	jsonOut(w, http.StatusOK, map[string]string{"command": taskOpenCommand(executable, s.app.store.directory, task.ID, s.app.hardwareAddress)})
}

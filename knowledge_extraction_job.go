package main

import "net/http"

// A paid extraction only starts after the user deliberately presses the AI
// extraction action. Routine harvesting uses already saved structured material.
func (s *Server) extractKnowledgeWithAI(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Confirm bool `json:"confirm"`
	}
	if !body(w, r, &request) {
		return
	}
	if !request.Confirm {
		fail(w, http.StatusBadRequest, "请确认将最近对话发送给当前模型进行知识提取。")
		return
	}
	if s.app.store.knowledgeExtractionNeedsDirectory() {
		fail(w, http.StatusConflict, "请先在知识库的积累设置中选择 Obsidian 根目录并启用 Markdown 同步。")
		return
	}
	if _, err := vaultRoot(s.app.store.vaultConfig()); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	task, err := s.app.store.task(r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusNotFound, "任务不存在")
		return
	}
	if task.Engine != "codex" && task.Engine != "claude" && task.Engine != "deepseek-harness" {
		fail(w, http.StatusBadRequest, "当前引擎尚不支持独立知识整理，请先切换到 Codex、Claude Code 或 Harness；已有候选仍可直接提取。")
		return
	}
	run, err := s.app.submit(task.ID, "请整理最近已完成对话，提取平台共识、系统知识和项目知识候选，并附标签、依据及适用条件。", "knowledge", knowledgeExtractionSource)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOut(w, http.StatusAccepted, map[string]any{"run": run})
}

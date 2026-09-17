package main

import (
	"context"
	"strconv"
	"strings"
)

// Codex session ids are UUIDv7, whose first 48 bits are the creation time.
// Engines with opaque ids report 0 and the task view drops the date.
func sessionStarted(id string) int64 {
	if len(id) < 15 || id[8] != '-' || id[13] != '-' || id[14] != '7' {
		return 0
	}
	raw, e := strconv.ParseUint(id[:8]+id[9:13], 16, 64)
	if e != nil {
		return 0
	}
	// Anything outside 2020..2100 is not a UUIDv7 timestamp.
	if raw < 1577836800000 || raw > 4102444800000 {
		return 0
	}
	return int64(raw)
}

// A task can run in a directory another agent tool already used. Those tools
// leave instruction files behind, and the engine reads them as if they were
// part of the current request, so the task view names them instead of leaving
// the user to work out why a reply ignored the message they just sent.
type ContextFile struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

var externalContextNames = map[string]string{
	".aha2-context":                   "AHA2 任务上下文",
	"agents.md":                       "AGENTS.md 仓库指令",
	"claude.md":                       "CLAUDE.md 仓库指令",
	"gemini.md":                       "GEMINI.md 仓库指令",
	".cursorrules":                    "Cursor 规则",
	".cursor":                         "Cursor 规则目录",
	".windsurfrules":                  "Windsurf 规则",
	".aider.conf.yml":                 "Aider 配置",
	".github/copilot-instructions.md": "Copilot 指令",
}

func externalContextFiles(ctx context.Context, task Task) []ContextFile {
	found := []ContextFile{}
	root, e := readWorkspace(ctx, task, "list", "")
	if e != nil {
		return found
	}
	github := false
	for _, item := range root.Items {
		name := strings.ToLower(item.Name)
		if item.Directory && name == ".github" {
			github = true
			continue
		}
		if label, ok := externalContextNames[name]; ok {
			found = append(found, ContextFile{Name: item.Name, Label: label})
		}
	}
	if !github {
		return found
	}
	nested, e := readWorkspace(ctx, task, "list", ".github")
	if e != nil {
		return found
	}
	for _, item := range nested.Items {
		if label, ok := externalContextNames[".github/"+strings.ToLower(item.Name)]; ok {
			found = append(found, ContextFile{Name: ".github/" + item.Name, Label: label})
		}
	}
	return found
}

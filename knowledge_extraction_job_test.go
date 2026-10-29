package main

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

type extractionJobProbe struct{ calls chan switchedCall }

func (p *extractionJobProbe) Run(_ context.Context, c Config, task Task, input string, emit func(string, string)) (string, string, error) {
	p.calls <- switchedCall{task: task, input: input, profile: c.EngineEnv}
	match := regexp.MustCompile(`<!-- duo-memory:([a-z0-9]+) -->`).FindStringSubmatch(input)
	if len(match) != 2 {
		return "", "缺少提取格式", nil
	}
	memory := taskMemory{Summary: "历史资料提取", KnowledgeCandidates: []knowledgeCandidate{{Layer: "system", ScopeKey: "测试方法", Title: "验证方法", Content: "先使用隔离环境验证。", Evidence: "历史记录说明使用隔离环境验证。", Applicability: "合成测试资料", Tags: []string{"验证"}}}}
	raw, _ := json.Marshal(memory)
	result := "历史记录说明使用隔离环境验证。\n" + memoryMarker(match[1]) + "\n" + string(raw) + "\n<!-- /duo-memory:" + match[1] + " -->"
	emit("session", "extraction-native-session")
	emit("assistant", result)
	return "extraction-native-session", result, nil
}

func TestAIKnowledgeExtractionRequiresConfiguredDirectoryAndConfirmation(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	request := toolsClient(t, a)
	endpoint := "/api/tasks/" + task.ID + "/knowledge/extract"
	request(endpoint, "POST", map[string]bool{"confirm": false}, 400)
	request(endpoint, "POST", map[string]bool{"confirm": true}, 409)
	var count int
	_ = a.store.QueryRow("SELECT count(*) FROM runs WHERE task_id=?", task.ID).Scan(&count)
	if count != 0 {
		t.Fatal("unconfirmed or unconfigured extraction queued a model run")
	}
}

func TestAIKnowledgeExtractionIsolatedAndCapturesWhenAutomationOff(t *testing.T) {
	for _, engine := range []string{"codex", "claude", "deepseek-harness"} {
		t.Run(engine, func(t *testing.T) {
			probe := &extractionJobProbe{calls: make(chan switchedCall, 1)}
			a := fixture(t, probe)
			task := taskFor(t, a)
			if engine == "deepseek-harness" {
				task = createHarnessStoredTask(t, a)
			} else if engine != "codex" {
				_, _ = a.store.Exec("UPDATE task_execution SET engine=? WHERE task_id=?", engine, task.ID)
			}
			_, _ = a.store.Exec("UPDATE tasks SET session='original-session' WHERE id=?", task.ID)
			raw, _ := json.Marshal(VaultConfig{Enabled: true, Directory: t.TempDir()})
			_ = a.store.set("knowledge_vault", string(raw))
			_ = a.store.set("automatic_knowledge", `{"capture":false,"organize":false,"recall":false}`)
			_, err := a.store.Exec("INSERT INTO runs(id,task_id,input,kind,source,status,result,created) VALUES('historical',?,'historical-input','chat','web','done','historical-result',1)", task.ID)
			if err != nil {
				t.Fatal(err)
			}
			request := toolsClient(t, a)
			request("/api/tasks/"+task.ID+"/knowledge/extract", "POST", map[string]bool{"confirm": true}, 202)
			call := <-probe.calls
			if call.task.Session != "" || call.task.ID != "" || !call.task.NoTools || call.task.Mode.Permission != "read" || len(call.task.Files) != 0 || !strings.Contains(call.input, "historical-result") || !strings.Contains(call.input, "knowledge_candidates") {
				t.Fatal("extraction did not use isolated bounded text", call.task)
			}
			waitUntil(t, func() bool {
				items, _ := a.store.curatedKnowledgeList(context.Background(), "system", "")
				return len(items) == 1
			})
			after, _ := a.store.task(task.ID)
			if after.Session != "original-session" {
				t.Fatal("extraction replaced the original native session", after.Session)
			}
			items, _ := a.store.curatedKnowledgeList(context.Background(), "system", "")
			if items[0].Status != "observed" || len(items[0].Tags) == 0 {
				t.Fatal("extraction must save tagged candidates only", items[0])
			}
		})
	}
}

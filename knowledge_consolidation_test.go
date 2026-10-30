package main

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

type consolidationProbe struct {
	calls   chan switchedCall
	release chan struct{}
	item    knowledgeCandidate
}

func (p *consolidationProbe) Run(ctx context.Context, c Config, task Task, input string, emit func(string, string)) (string, string, error) {
	p.calls <- switchedCall{task: task, input: input, profile: c.EngineEnv}
	if p.release != nil {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case <-p.release:
		}
	}
	match := regexp.MustCompile(`<!-- duo-memory:([a-z0-9]+) -->`).FindStringSubmatch(input)
	if len(match) != 2 {
		return "", "missing envelope", nil
	}
	raw, _ := json.Marshal(taskMemory{Summary: "主题合并稿已生成，未验证分歧仍保留", KnowledgeCandidates: []knowledgeCandidate{p.item}})
	result := p.item.Content + "\n\n" + p.item.Evidence + "\n" + memoryMarker(match[1]) + "\n" + string(raw) + "\n<!-- /duo-memory:" + match[1] + " -->"
	emit("session", "isolated-consolidation-session")
	emit("assistant", result)
	return "isolated-consolidation-session", result, nil
}

func consolidationNote(t *testing.T, a *App, count int) CuratedKnowledge {
	t.Helper()
	v := curatedFixture(t, a, "project", "合成平台", "verified")
	for i := 0; i < count; i++ {
		p := KnowledgeRevision{Key: hash(fmt.Sprintf("topic-source-%d", i)), Content: fmt.Sprintf("修订 %d：先检查导出权限，历史环境与当前设置是否一致仍有分歧。", i), Evidence: "历史测试记录，不是本轮新验证", Applicability: "仅合成回归", SourceID: fmt.Sprintf("run:source-%d", i), SourceTaskID: "historical-task"}
		var err error
		v, _, err = a.store.queueKnowledgeRevision(context.Background(), v.ID, p)
		if err != nil {
			t.Fatal(err)
		}
	}
	return v
}

func consolidationItem(v CuratedKnowledge) knowledgeCandidate {
	return knowledgeCandidate{Layer: v.Layer, ScopeKey: v.ScopeKey, Title: v.Title, Content: "## 问题与办法\n先检查 NFS 服务，再核对导出权限。\n\n## 未解决分歧\n历史资料对环境设置存在不同说法，尚未在当前环境复核。", Evidence: "仅据历史正文和修订合成，未调用真实模型或设备验证。", Applicability: "仅合成回归；当前环境需要另行验证", Tags: []string{"NFS"}}
}

func TestKnowledgeConsolidationRequiresExplicitScopeAndConfirmation(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	a.updating.Store(true)
	base := consolidationNote(t, a, 1)
	a.updating.Store(false)
	request := toolsClient(t, a)
	endpoint := "/api/library/curated/" + base.ID + "/consolidate"
	request(endpoint, "POST", map[string]any{"task_id": task.ID, "revision": base.Revision}, 400)
	request(endpoint, "POST", map[string]any{"task_id": task.ID, "revision": base.Revision, "confirm": true}, 409)
	organizationVault(t, a, VaultConfig{Enabled: true, Directory: t.TempDir()})
	a.updating.Store(false)
	request(endpoint, "POST", map[string]any{"task_id": task.ID, "revision": base.Revision - 1, "confirm": true}, 409)
	request(endpoint, "POST", map[string]any{"task_id": "missing", "revision": base.Revision, "confirm": true}, 404)
	large := base
	large.Content = strings.Repeat("长", 4100)
	large, _, err := a.store.saveCurated(context.Background(), large, false)
	if err != nil {
		t.Fatal(err)
	}
	request(endpoint, "POST", map[string]any{"task_id": task.ID, "revision": large.Revision, "confirm": true}, 400)
	var count int
	_ = a.store.QueryRow("SELECT count(*) FROM knowledge_topic_jobs").Scan(&count)
	if count != 0 {
		t.Fatal("invalid requests created snapshots or paid jobs", count)
	}
}

func TestKnowledgeConsolidationFullTopicIsolatedAcceptsAndPreservesSources(t *testing.T) {
	for _, engine := range []string{"codex", "claude", "deepseek-harness"} {
		t.Run(engine, func(t *testing.T) {
			probe := &consolidationProbe{calls: make(chan switchedCall, 1)}
			a := fixture(t, probe)
			task := taskFor(t, a)
			if engine == "deepseek-harness" {
				task = createHarnessStoredTask(t, a)
			} else {
				_, _ = a.store.Exec("UPDATE task_execution SET engine=? WHERE task_id=?", engine, task.ID)
			}
			_, _ = a.store.Exec("UPDATE tasks SET session='source-native-session' WHERE id=?", task.ID)
			organizationVault(t, a, VaultConfig{Enabled: true, Directory: t.TempDir(), RootMode: "direct", HumanLayout: true})
			base := consolidationNote(t, a, 8)
			probe.item = consolidationItem(base)
			// No unprocessed chats are needed: this action reviews an existing topic.
			preview, _ := a.store.knowledgeSynthesisPreview(context.Background(), task.ID)
			if preview.Remaining != 0 {
				t.Fatal(preview)
			}
			a.updating.Store(false)
			request := toolsClient(t, a)
			request("/api/library/curated/"+base.ID+"/consolidate", "POST", map[string]any{"task_id": task.ID, "revision": base.Revision, "confirm": true}, 202)
			call := <-probe.calls
			marker := "选定主题的完整历史快照（含全部待修订；不是本轮新事实）：\n"
			var snapshot CuratedKnowledge
			_, encoded, _ := strings.Cut(call.input, marker)
			_ = json.Unmarshal([]byte(encoded), &snapshot)
			if call.task.ID != "" || call.task.Session != "" || !call.task.NoTools || call.task.Mode.Permission != "read" || len(call.task.Files) != 0 || snapshot.Content != base.Content || !strings.Contains(call.input, "source-7") || !strings.Contains(call.input, "未解决分歧") || strings.Contains(call.input, "尚未整理资料") {
				t.Fatal("topic consolidation did not use the full isolated snapshot", call.task, call.input)
			}
			var queued CuratedKnowledge
			waitUntil(t, func() bool {
				queued, _ = a.store.curatedKnowledge(context.Background(), base.ID)
				return len(queued.PendingRevisions) == 9
			})
			if queued.Content != base.Content || queued.Status != "verified" || len(queued.PendingRevisions[8].Supersedes) != 8 || queued.PendingRevisions[8].BaseChanged {
				t.Fatal("AI changed the published knowledge or lost revision coverage", queued)
			}
			merged := queued.PendingRevisions[8]
			accepted, err := a.store.resolveKnowledgeRevision(context.Background(), queued.ID, merged.Key, queued.Revision, true)
			if err != nil || accepted.Status != "observed" || accepted.Content != probe.item.Content || len(accepted.PendingRevisions) != 0 || len(accepted.RevisionHistory) != 9 || accepted.SourceID != base.SourceID {
				t.Fatal("acceptance did not retain sources and downgrade verification", accepted, err)
			}
			previous := accepted.RevisionHistory[8].Previous
			if previous == nil || previous.Content != base.Content || previous.Evidence != base.Evidence || previous.Applicability != base.Applicability || previous.Status != base.Status || previous.SourceID != base.SourceID || previous.SourceTaskID != base.SourceTaskID || previous.SourceHash != base.SourceHash || previous.Revision != queued.Revision {
				t.Fatal("adoption lost the exact pre-merge published note", previous, base)
			}
			storedTask, _ := a.store.task(task.ID)
			if storedTask.Session != "source-native-session" {
				t.Fatal("topic review replaced the task native session", storedTask.Session)
			}
			var consumed int
			_ = a.store.QueryRow("SELECT count(*) FROM knowledge_ai_sources WHERE task_id=?", task.ID).Scan(&consumed)
			if consumed != 0 {
				t.Fatal("topic review consumed the separate history cursor", consumed)
			}
			jobs, _ := a.store.runs(task.ID)
			for _, run := range jobs {
				report, e := a.store.extractTaskKnowledge(context.Background(), task.ID, run.ID, nil)
				if e != nil || report.Revisions != 0 || report.Created != 0 || report.Duplicates != 1 {
					t.Fatal("re-harvesting a resolved merge resurrected revisions", report, e)
				}
			}
			files, err := a.store.curatedDocuments(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			for _, d := range files {
				if d.Kind != "curated" {
					continue
				}
				raw, e := renderVaultFile(d, nil)
				if e != nil {
					t.Fatal(e)
				}
				file, e := parseVaultFile(d.Path, raw)
				if e != nil || len(file.Document.RevisionHistory) != 9 || file.Document.Hash != d.Hash || len(file.Document.RevisionHistory[8].Supersedes) != 8 || file.Document.RevisionHistory[8].Previous == nil || file.Document.RevisionHistory[8].Previous.Content != base.Content {
					t.Fatal("portable merge sources or document identity were lost", file, e)
				}
			}
		})
	}
}

func TestKnowledgeConsolidationConcurrentEditAndDiscardCannotBeOverwritten(t *testing.T) {
	for _, action := range []string{"edit-body", "discard-source"} {
		t.Run(action, func(t *testing.T) {
			probe := &consolidationProbe{calls: make(chan switchedCall, 1), release: make(chan struct{})}
			a := fixture(t, probe)
			task := taskFor(t, a)
			organizationVault(t, a, VaultConfig{Enabled: true, Directory: t.TempDir()})
			base := consolidationNote(t, a, 2)
			probe.item = consolidationItem(base)
			a.updating.Store(false)
			request := toolsClient(t, a)
			var response struct {
				Run Run `json:"run"`
			}
			_ = json.Unmarshal(request("/api/library/curated/"+base.ID+"/consolidate", "POST", map[string]any{"task_id": task.ID, "revision": base.Revision, "confirm": true}, 202), &response)
			<-probe.calls
			if action == "edit-body" {
				base.Content = "用户在等待模型时维护的新正文，必须保留。"
				if _, _, err := a.store.saveCurated(context.Background(), base, false); err != nil {
					t.Fatal(err)
				}
			} else if _, err := a.store.resolveKnowledgeRevision(context.Background(), base.ID, base.PendingRevisions[0].Key, base.Revision, false); err != nil {
				t.Fatal(err)
			}
			close(probe.release)
			waitUntil(t, func() bool {
				runs, _ := a.store.runs(task.ID)
				return len(runs) == 1 && runs[0].Status == "done"
			})
			var stored CuratedKnowledge
			if action == "edit-body" {
				waitUntil(t, func() bool {
					stored, _ = a.store.curatedKnowledge(context.Background(), base.ID)
					return len(stored.PendingRevisions) == 3
				})
				p := stored.PendingRevisions[2]
				if !p.BaseChanged {
					t.Fatal("changed baseline was not shown as a conflict", stored)
				}
				if _, err := a.store.resolveKnowledgeRevision(context.Background(), stored.ID, p.Key, stored.Revision, true); err == nil || !strings.Contains(err.Error(), "原正文已变化") {
					t.Fatal("model overwrote an edited body", err)
				}
				d := curatedDocument(stored)
				raw, _ := renderVaultFile(d, nil)
				parsed, err := parseVaultFile(d.Path, raw)
				if err != nil || parsed.Document.Hash != d.Hash || validateCuratedIdentity(d, parsed.Document) != nil {
					t.Fatal("display-only conflict flags changed portable hashes", err, raw)
				}
			} else {
				// Explicit re-harvesting also cannot reintroduce a discarded source.
				report, err := a.store.extractTaskKnowledge(context.Background(), task.ID, response.Run.ID, nil)
				stored, _ = a.store.curatedKnowledge(context.Background(), base.ID)
				if err != nil || report.Skipped != 1 || len(stored.PendingRevisions) != 1 || stored.Content != base.Content {
					t.Fatal("discarded source was reintroduced through an old AI snapshot", report, stored, err)
				}
			}
		})
	}
}

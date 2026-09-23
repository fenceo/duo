package main

import "testing"

func TestFeishuNewTaskUsesEngineSpecificDefaultModel(t *testing.T) {
	for _, engine := range []string{"codex", "claude", "deepseek-harness"} {
		t.Run(engine, func(t *testing.T) {
			a := fixture(t, &fakeRunner{})
			c := a.config.get()
			e := &c.Environments[0]
			e.DefaultEngine = engine
			e.Model, e.ClaudeModel, e.HarnessModel = "codex-fixture", "claude-fixture", "harness-fixture"
			if err := a.config.save(c); err != nil {
				t.Fatal(err)
			}
			// Empty input creates only a local draft; no Feishu or model request.
			if _, err := a.feishu.handle("fixture-chat", "/新建 模型选择 | "); err != nil {
				t.Fatal(err)
			}
			task, err := a.store.task(a.bound("fixture-chat"))
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]string{"codex": "codex-fixture", "claude": "claude-fixture", "deepseek-harness": "harness-fixture"}[engine]
			if task.Engine != engine || task.Model != want {
				t.Fatalf("wrong task engine/model: %s / %s", task.Engine, task.Model)
			}
		})
	}
}

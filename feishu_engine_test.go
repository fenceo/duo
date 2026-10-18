package main

import (
	"strings"
	"testing"
)

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

func TestFeishuPlainTextCreatesTaskWhenUnbound(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	reply, err := a.feishu.handle("chat", "帮我检查串口日志")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "已新建任务") {
		t.Fatal(reply)
	}
	id := a.bound("chat")
	if id == "" {
		t.Fatal("plain text did not bind a task")
	}
	task, err := a.store.task(id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Title == "" || len([]rune(task.Title)) > 41 {
		t.Fatal("auto title out of range:", task.Title)
	}
	runs, _ := a.store.runs(id)
	if len(runs) != 1 {
		t.Fatal("plain text did not submit a run", len(runs))
	}
}

func TestFeishuNewCommandAliasesAndAutoTitle(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	for _, alias := range []string{"新建 修复登录", "新建任务 修复登录", "/新建 修复登录", "新任务 修复登录"} {
		chat := "chat-" + alias
		if _, err := a.feishu.handle(chat, alias); err != nil {
			t.Fatal(alias, err)
		}
		id := a.bound(chat)
		if id == "" {
			t.Fatal(alias, "no task bound")
		}
		task, err := a.store.task(id)
		if err != nil {
			t.Fatal(err)
		}
		if task.Title != "修复登录" {
			t.Fatal(alias, "title:", task.Title)
		}
	}
}

func TestFeishuUnboundContinueStillRequiresSelection(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	for _, text := range []string{"继续", "继续任务", "请继续工作", "停止", "停止任务"} {
		reply, err := a.feishu.handle("chat", text)
		if err != nil {
			t.Fatal(text, err)
		}
		if strings.Contains(reply, "已新建任务") {
			t.Fatal(text, "must not create:", reply)
		}
		if !strings.Contains(reply, "/切换 "+task.ID[:8]) && !strings.Contains(reply, "尚未选择任务") {
			t.Fatal(text, reply)
		}
	}
	tasks, _ := a.store.tasks()
	if len(tasks) != 1 {
		t.Fatal("unbound continue created a task")
	}
}

func TestFeishuCreateUsageAndAutoTitleBounds(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	reply, err := a.feishu.handle("chat", "/新建")
	if err != nil || !strings.Contains(reply, "请直接发送要做的事") {
		t.Fatal(reply, err)
	}
	if got := feishuAutoTitle("  帮我\n检查 串口  "); got != "帮我 检查 串口" {
		t.Fatal("auto title:", got)
	}
	long := strings.Repeat("很长的标题", 20)
	got := feishuAutoTitle(long)
	if len([]rune(got)) != 41 || !strings.HasSuffix(got, "…") {
		t.Fatal("auto title not truncated:", len([]rune(got)), got)
	}
	if feishuAutoTitle("   ") != "新任务" {
		t.Fatal("empty auto title fallback")
	}
}

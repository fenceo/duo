package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskActionsCardFollowsBinding(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	c := a.config.get()
	c.Feishu = FeishuConfig{Enabled: true, AppID: "test", Secret: "test", Owner: "owner"}
	a.config.save(c)

	// Out of task: create + list only.
	card, err := a.feishu.taskActionsCard("chat")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(card)
	s := string(raw)
	if !strings.Contains(s, "未进入任务") || !strings.Contains(s, "新建任务") || !strings.Contains(s, "任务列表") {
		t.Fatal("unbound card:", s)
	}
	if strings.Contains(s, "继续执行") || strings.Contains(s, "退出任务") {
		t.Fatal("unbound card leaks in-task actions")
	}

	// In task: continue/stop + view/knowledge + switch/exit.
	if err = a.bind("chat", task.ID); err != nil {
		t.Fatal(err)
	}
	card, err = a.feishu.taskActionsCard("chat")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(card)
	s = string(raw)
	if !strings.Contains(s, "已进入任务") || !strings.Contains(s, task.Title) {
		t.Fatal("bound card missing task:", s)
	}
	for _, want := range []string{"查看详情", "整理知识", "切换任务", "退出任务"} {
		if !strings.Contains(s, want) {
			t.Fatal("bound card missing", want, s)
		}
	}
	if !strings.Contains(s, "继续执行") {
		t.Fatal("bound idle card should offer continue:", s)
	}
	// Menu entry routes to the same card.
	a.store.Exec("INSERT OR IGNORE INTO settings VALUES('feishu_chat','chat')")
	if err = a.feishu.receiveMenu("menu-x", "owner", "jianzuo.menu"); err != nil {
		t.Fatal(err)
	}
	var n int
	a.store.QueryRow("SELECT count(*) FROM outbox_cards").Scan(&n)
	if n == 0 {
		t.Fatal("menu did not enqueue action card")
	}
}

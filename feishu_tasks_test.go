package main

import (
	"strings"
	"testing"
)

func TestFeishuContinueRequiresSelectionAndViewsExistingTask(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	for _, command := range []string{"/继续", "继续", "继续任务", "请继续工作"} {
		reply, err := a.feishu.handle("chat", command)
		if err != nil || !strings.Contains(reply, "/切换 "+task.ID[:8]) {
			t.Fatal(reply, err)
		}
	}
	tasks, _ := a.store.tasks()
	if len(tasks) != 1 {
		t.Fatal("unbound continue created a task")
	}
	runs, _ := a.store.runs(task.ID)
	if len(runs) != 0 {
		t.Fatal("unbound continue ran")
	}
	reply, err := a.feishu.handle("chat", "/查看 "+task.ID[:8])
	if err != nil || !strings.Contains(reply, task.Workspace) {
		t.Fatal(reply, err)
	}
	if a.bound("chat") != "" {
		t.Fatal("view changed selected task")
	}
	reply, err = a.feishu.handle("chat", "/切换 "+task.ID[:8])
	if err != nil || a.bound("chat") != task.ID {
		t.Fatal(reply, err)
	}
	reply, err = a.feishu.handle("chat", "/继续")
	if err != nil {
		t.Fatal(reply, err)
	}
	waitUntil(t, func() bool { r, _ := a.store.runs(task.ID); return len(r) == 1 && r[0].Status == "done" })
	reply, err = a.feishu.handle("chat", "/查看")
	if err != nil || !strings.Contains(reply, "reply: 继续") {
		t.Fatal(reply, err)
	}
	tasks, _ = a.store.tasks()
	if len(tasks) != 1 {
		t.Fatal("resume forked task")
	}
}

func TestFeishuContinueByIDAndListSearch(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	reply, err := a.feishu.handle("chat", "/继续 "+task.ID[:8])
	if err != nil || a.bound("chat") != task.ID {
		t.Fatal(reply, err)
	}
	waitUntil(t, func() bool { r, _ := a.store.runs(task.ID); return len(r) == 1 && r[0].Status == "done" })
	reply, err = a.feishu.handle("chat", "/任务 missing-title")
	if err != nil || strings.Contains(reply, "选择：/切换") {
		t.Fatal(reply, err)
	}
}

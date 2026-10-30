package main

import (
	"context"
	"strings"
	"testing"
)

func TestKnowledgeLookupRelevantBoundedAndRetiredExcluded(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	task.Session = "ongoing-native-session"
	for _, spec := range []struct{ title, status string }{{"NFS挂载排查", "verified"}, {"NFS旧方法", "stale"}, {"显示驱动排查", "observed"}} {
		v := CuratedKnowledge{Layer: "system", ScopeKey: "Linux", Title: spec.title, Content: strings.Repeat(spec.title+"检查目录权限。", 600), Status: spec.status, Applicability: "合成环境", Evidence: "合成测试资料", Tags: []string{spec.title}}
		if _, _, err := a.store.saveCurated(context.Background(), v, true); err != nil {
			t.Fatal(err)
		}
	}
	text, ids, err := a.store.knowledgeLookupContext(context.Background(), task, "NFS挂载排查")
	if err != nil || len(ids) != 1 || !strings.Contains(text, "NFS挂载排查") || strings.Contains(text, "NFS旧方法") || strings.Contains(text, "显示驱动") || !strings.Contains(text, `"truncated":true`) || len([]rune(text)) > 2600 {
		t.Fatal("unexpected lookup", len(text), ids, err)
	}
	if text, _, err = a.store.knowledgeLookupContext(context.Background(), task, "无关问题"); err != nil || text != "" {
		t.Fatal("unrelated note injected", text, err)
	}
	if err = a.store.set("automatic_knowledge", `{"capture":true,"recall":false,"organize":true}`); err != nil {
		t.Fatal(err)
	}
	if text, _, err = a.store.knowledgeLookupContext(context.Background(), task, "NFS挂载排查"); err != nil || text != "" {
		t.Fatal("opt out ignored", text, err)
	}
}

func TestKnowledgeLookupCandidatesStayUnverified(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	task := taskFor(t, a)
	v := CuratedKnowledge{Layer: "project", ScopeKey: "375平台", Title: "DDR排查", Content: "历史线索，不是当前结果。", Status: "observed", Applicability: "原项目条件", Evidence: "仅历史回复"}
	if _, _, err := a.store.saveCurated(context.Background(), v, true); err != nil {
		t.Fatal(err)
	}
	text, ids, err := a.store.knowledgeLookupContext(context.Background(), task, "检查375平台的DDR")
	if err != nil || len(ids) != 1 || !strings.Contains(text, `"status":"observed"`) || !strings.Contains(text, "只可作为调查线索") {
		t.Fatal(text, ids, err)
	}
}

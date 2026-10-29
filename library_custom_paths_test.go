package main

import (
	"context"
	"encoding/json"
	"path"
	"strings"
	"testing"
)

func libraryCustomFolderConfig(t *testing.T, a *App) VaultConfig {
	t.Helper()
	c := VaultConfig{Enabled: false, Directory: t.TempDir(), ConsensusDirectory: "个人资料/平台约定", SystemDirectory: "学习资料/系统知识", ProjectDirectory: "工作资料/项目知识"}
	if err := prepareKnowledgeFolders(&c); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.store.set("knowledge_vault", string(raw)); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLibraryCustomLayerDocumentsReadableBeforeFirstVaultSync(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	c := libraryCustomFolderConfig(t, a)
	for _, layer := range []string{"consensus", "system", "project"} {
		t.Run(layer, func(t *testing.T) {
			v := curatedFixture(t, a, layer, "合成主题或项目", "observed")
			want := configuredCuratedPath(c, curatedDocument(v))
			doc, err := a.store.libraryDocument(context.Background(), "curated:"+v.ID)
			if err != nil || doc.Path != want {
				t.Fatal("direct entry ignores custom folder before sync", doc, want, err)
			}
			entry, err := a.store.libraryDocument(context.Background(), "document:"+want)
			if err != nil || entry.ID != "curated:"+v.ID || entry.Path != want || entry.Content != v.Content {
				t.Fatal("custom reading link unavailable before sync", entry, err)
			}
			indexPath := path.Join(configuredKnowledgeFolder(c, layer), "README.md")
			index, err := a.store.libraryDocument(context.Background(), "document:"+indexPath)
			link := strings.ReplaceAll(notebookLink(indexPath, want), " ", "%20")
			if err != nil || index.ID != "curated-index:"+layer || index.Path != indexPath || !strings.Contains(index.Content, link) {
				t.Fatal("custom reading index unavailable or uses a stale path", index, err)
			}
			_, citation, _, err := a.store.libraryReference(context.Background(), "curated:"+v.ID, "")
			if err != nil || !strings.Contains(citation, want) {
				t.Fatal("citation ignores configured path", citation, err)
			}
		})
	}
	for _, table := range []string{"library_links", "library_files"} {
		var count int
		if err := a.store.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("fixture unexpectedly synchronized", table, count, err)
		}
	}
	for _, relative := range []string{"../private.md", "/absolute.md", "C:/private.md", "个人资料/平台约定/../private.md", "个人资料\\平台约定\\private.md", "个人资料/平台约定/\x00secret.md"} {
		if _, err := a.store.libraryDocument(context.Background(), "document:"+relative); err == nil || !strings.Contains(err.Error(), "越界") {
			t.Fatal("unsafe document link accepted", relative, err)
		}
	}
}

func TestLibraryCustomLayerReadingKeepsTrackedPathBeforeFileIndex(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	c := libraryCustomFolderConfig(t, a)
	v := curatedFixture(t, a, "project", "新的项目", "observed")
	tracked := "旧资料/人工归档/保留名字.md"
	if _, err := a.store.Exec("INSERT INTO library_links(id,path,local_hash,file_hash) VALUES(?,?,?,?)", "curated:"+v.ID, tracked, "fixture-local-hash", "fixture-file-hash"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"curated:" + v.ID, "document:" + tracked} {
		doc, err := a.store.libraryDocument(context.Background(), id)
		if err != nil || doc.ID != "curated:"+v.ID || doc.Path != tracked || doc.Content != v.Content {
			t.Fatal("tracked path lost before file index", id, doc, err)
		}
	}
	indexPath := path.Join(configuredKnowledgeFolder(c, "project"), "README.md")
	index, err := a.store.libraryDocument(context.Background(), "document:"+indexPath)
	if err != nil || !strings.Contains(index.Content, notebookLink(indexPath, tracked)) {
		t.Fatal("index does not use the tracked actual path", index, err)
	}
	if _, err := a.store.libraryDocument(context.Background(), "document:"+configuredCuratedPath(c, curatedDocument(v))); err == nil {
		t.Fatal("unwritten canonical path was presented as a second tracked note")
	}
}

func TestLibraryTaskNotebookDoesNotLoadUnrelatedCuratedDocuments(t *testing.T) {
	a := fixture(t, &fakeRunner{})
	libraryCustomFolderConfig(t, a)
	task := taskFor(t, a)
	workspaceMemoryRun(t, a, task, "synthetic notebook requirement", "synthetic notebook result", "done")
	// Malformed unrelated formal data would fail curatedDocuments. Task notebook
	// navigation must not perform that scan when the path is outside layer roots.
	if _, err := a.store.Exec("INSERT INTO curated_knowledge(id,layer,scope_key,source_id,document,revision,updated) VALUES('unrelated-broken','project','','','{',1,1)"); err != nil {
		t.Fatal(err)
	}
	docs, err := a.store.notebookDocuments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, doc := range docs {
		if doc.TaskID == task.ID && doc.Kind == "task" {
			loaded, err := a.store.libraryDocument(context.Background(), "document:"+doc.Path)
			if err != nil || loaded.TaskID != task.ID || loaded.Content != doc.Content {
				t.Fatal("task navigation loaded unrelated curated data", loaded, err)
			}
			return
		}
	}
	t.Fatal("task notebook fixture has no main document")
}

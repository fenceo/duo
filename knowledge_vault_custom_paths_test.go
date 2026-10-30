package main

import (
	"context"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// Imported notes can have a project AND topic directory. Both their own
// relative provenance and the user's landing pages must survive a real sync.
func TestCuratedVaultSyncPreservesImportedNestedPaths(t *testing.T) {
	for _, layer := range []string{"system", "project"} {
		for _, taskFolders := range []bool{false, true} {
			name := layer + "/tracked"
			if taskFolders {
				name = layer + "/task-folders"
			}
			t.Run(name, func(t *testing.T) {
				a := fixture(t, &fakeRunner{})
				a.updating.Store(true)
				noLegacy := false
				c := VaultConfig{RootMode: "direct", HumanLayout: true, IncludeLegacy: &noLegacy, TaskFolders: taskFolders, Enabled: true, Directory: t.TempDir()}
				config, err := json.Marshal(c)
				if err != nil {
					t.Fatal(err)
				}
				if err = a.store.set("knowledge_vault", string(config)); err != nil {
					t.Fatal(err)
				}
				v := CuratedKnowledge{ID: uid(), Layer: layer, ScopeKey: "合成平台", Title: "内存验证结论", Content: "# 内存验证结论\n\n仅合成来源。\n\n## 来源\n\n[合成日志](../证据/合成日志.md)", Evidence: "仅合成夹具，无真实模型和硬件调用。", Applicability: "仅合成回归", SourceID: "external:nested-path-" + layer, Status: "observed", Revision: 1}
				doc := curatedDocument(v)
				raw, err := renderVaultFile(doc, nil)
				if err != nil {
					t.Fatal(err)
				}
				folder := configuredKnowledgeFolder(c, layer)
				project := path.Join(folder, v.ScopeKey)
				topic := path.Join(project, "内存与性能")
				relative := path.Join(topic, v.Title+".md")
				projectIndex := path.Join(project, "首页.md")
				topicIndex := path.Join(topic, "首页.md")
				evidence := path.Join(project, "证据/合成日志.md")
				originals := map[string]string{
					"首页.md":       "# 用户知识首页\n\n这是整理脚本风格的人类阅读入口，不由Duo覆盖。\n",
					"Duo知识库索引.md": "# 用户已有的同名资料\n\n应选择另一个确定名称。\n",
					relative:      raw,
					projectIndex:  "# 合成平台\n\n[内存验证结论](内存与性能/内存验证结论.md)\n",
					topicIndex:    "# 内存与性能\n\n[内存验证结论](内存验证结论.md)\n",
					evidence:      "# 合成日志\n\n仅用于离线回归。\n",
				}
				for relative, content := range originals {
					filename := filepath.Join(c.Directory, filepath.FromSlash(relative))
					if err = os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
						t.Fatal(err)
					}
					if err = os.WriteFile(filename, []byte(content), 0600); err != nil {
						t.Fatal(err)
					}
				}
				for pass := 0; pass < 2; pass++ {
					report := syncTestVault(t, a)
					if len(report.Conflicts) != 0 || len(report.Warnings) != 0 {
						t.Fatal("custom hierarchy caused a false sync conflict", pass, report)
					}
					if pass == 0 && report.Imported != 1 {
						t.Fatal("nested candidate was not imported once", report)
					}
					if pass == 1 && report.Imported != 0 {
						t.Fatal("second sync duplicated the nested candidate", report)
					}
					for relative, want := range originals {
						got, err := os.ReadFile(filepath.Join(c.Directory, filepath.FromSlash(relative)))
						if err != nil || string(got) != want {
							t.Fatal("sync moved or rewrote a note, landing page or provenance", relative, err)
						}
					}
					stored, err := a.store.libraryDocument(context.Background(), doc.ID)
					if err != nil || stored.Path != relative {
						t.Fatal("citation lost the imported path", stored.Path, err)
					}
					home, err := a.store.libraryDocument(context.Background(), "curated-index:home")
					if err != nil || home.Path == "首页.md" || home.Path == "Duo知识库索引.md" || !strings.HasPrefix(home.Path, "Duo知识库索引-") {
						t.Fatal("generated home did not expose its actual independent path", home.Path, err)
					}
					homeRaw := readTestFile(t, filepath.Join(c.Directory, filepath.FromSlash(home.Path)))
					parsedHome, err := parseVaultFile(home.Path, homeRaw)
					if err != nil || parsedHome.Document.ID != home.ID {
						t.Fatal("independent root home is missing or has the wrong identity", err)
					}
				}
				homeCopies, err := filepath.Glob(filepath.Join(c.Directory, "Duo知识库索引-*.md"))
				if err != nil || len(homeCopies) != 1 {
					t.Fatal("second sync duplicated the generated root index", homeCopies, err)
				}
				flat := configuredCuratedPath(c, doc)
				if _, err := os.Stat(filepath.Join(c.Directory, filepath.FromSlash(flat))); !os.IsNotExist(err) {
					t.Fatal("a second flattened copy was created", flat, err)
				}
				index := readTestFile(t, filepath.Join(c.Directory, filepath.FromSlash(folder), "README.md"))
				if !strings.Contains(index, knowledgeMarkdownLink(path.Join(folder, "README.md"), path.Join(project, "README.md"))) {
					t.Fatal("generated category index lost its project entry")
				}
				projectGenerated := readTestFile(t, filepath.Join(c.Directory, filepath.FromSlash(project), "README.md"))
				if !strings.Contains(projectGenerated, knowledgeMarkdownLink(path.Join(project, "README.md"), relative)) {
					t.Fatal("generated project index did not follow the actual nested note")
				}
			})
		}
	}
}

func TestCuratedCustomPathDetectionRetainsManagedSafeMoves(t *testing.T) {
	previous := curatedDocument(CuratedKnowledge{ID: uid(), Layer: "project", ScopeKey: "原项目", Title: "原标题"})
	for _, human := range []bool{false, true} {
		c := VaultConfig{HumanLayout: human, ProjectDirectory: "资料/原分类"}
		managed := configuredCuratedPath(c, previous)
		if curatedCustomTrackedPath(c, previous, managed) {
			t.Fatal("Duo canonical path was mistaken for a custom import", human, managed)
		}
		// Changing to the parent root must remain an explicit safe migration,
		// even though the old path is also below the new configured root.
		c.ProjectDirectory = "资料"
		if curatedCustomTrackedPath(c, previous, managed) {
			t.Fatal("explicit parent-root migration was suppressed", human, managed)
		}
	}
}

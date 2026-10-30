package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode"
)

func organizerSourceHashes(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		out[filepath.ToSlash(relative)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal("could not hash synthetic organizer sources", err)
	}
	return out
}

func organizerPython(t *testing.T) string {
	t.Helper()
	names := []string{"python3", "python"}
	if runtime.GOOS == "windows" {
		names = []string{"python", "python3"}
	}
	for _, name := range names {
		if binary, err := exec.LookPath(name); err == nil {
			return binary
		}
	}
	t.Skip("Python 3 is required for the offline organizer integration fixture")
	return ""
}

// This exercises the real offline script, Go Markdown parser, scanner and
// curated importer together. Every input/output and database is synthetic.
func TestKnowledgeVaultOrganizerLegacyInsightImportsAsProjectCandidate(t *testing.T) {
	python := organizerPython(t)
	root := t.TempDir()
	source := filepath.Join(root, "legacy", "Duo")
	output := filepath.Join(root, "organized")
	report := filepath.Join(root, "reports", "preview.json")
	if err := os.MkdirAll(filepath.Join(source, "knowledge"), 0700); err != nil {
		t.Fatal(err)
	}
	const title = "缓存归零与RSS观测"
	const evidence = "合成样本日志显示缓存已归零，RSS未立即下降。"
	const applicability = "仅适用于375平台的合成日志，不代表真实设备已验证。"
	raw := fmt.Sprintf("---\nduo_format: 1\nduo_id: \"insight:legacy-run-1\"\nduo_kind: \"insight\"\nduo_task: \"task-375\"\nduo_task_title: \"375调试\"\nduo_updated: 1792416579737\ntitle: %q\nstatus: \"observed\"\ntags: [\"内存\", \"Linux\", \"DDR\"]\n---\n\n# %s\n\n## 问题\n\n缓存归零后RSS是否立即下降？\n\n## 结论\n\n逻辑缓存归零不能单独证明物理内存已释放。\n\n## 依据\n\n%s\n\n## 适用条件\n\n%s\n\n## 来源\n\n原任务的合成执行记录。\n", title, title, evidence, applicability)
	if err := os.WriteFile(filepath.Join(source, "knowledge", "legacy-run-1.md"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	// A note with no literal evidence remains a review note, not a formal entry.
	plain := "---\ntitle: \"待确认的知识整理方法\"\nduo_kind: \"insight\"\nduo_task_title: \"尚未归属项目\"\ntags: [\"知识整理\"]\n---\n\n# 待确认的知识整理方法\n\n仅有计划，尚无实际检查结果。\n"
	if err := os.WriteFile(filepath.Join(source, "knowledge", "without-evidence.md"), []byte(plain), 0600); err != nil {
		t.Fatal(err)
	}
	before := organizerSourceHashes(t, source)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "scripts/Organize-KnowledgeVault.py", "--source", source, "--output", output, "--report", report, "--layout", "source-notes", "--apply")
	hideCommand(cmd)
	if log, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthetic offline organizer failed: %v\n%s", err, log)
	}
	if after := organizerSourceHashes(t, source); !reflect.DeepEqual(before, after) {
		t.Fatal("offline organization changed a source file")
	}
	relative := "项目知识/375平台/内存与性能/" + title + ".md"
	organizedRaw, err := os.ReadFile(filepath.Join(output, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatal("expected human-readable project note was not created", err)
	}
	parsed, err := parseVaultFile(relative, string(organizedRaw))
	if err != nil {
		t.Fatal("organized Markdown is not compatible with Go Vault parsing", err)
	}
	doc := parsed.Document
	if doc.Kind != "curated" || doc.Layer != "project" || doc.ScopeKey != "375平台" || doc.Title != title || doc.Status != "observed" {
		t.Fatal("organized insight did not retain its project candidate metadata")
	}
	if doc.Evidence != evidence || doc.Applicability != applicability || doc.SourceTaskID != "task-375" || doc.SourceHash != before["knowledge/legacy-run-1.md"] || !strings.HasPrefix(doc.SourceID, "external:legacy-vault/knowledge/legacy-run-1.md#") {
		t.Fatal("organized insight lost its literal evidence, applicability, or immutable provenance")
	}
	for _, tag := range []string{"内存", "linux", "ddr"} {
		if !strings.Contains("|"+strings.Join(doc.Tags, "|")+"|", "|"+tag+"|") {
			t.Fatalf("organized insight lost required tag %q", tag)
		}
	}
	if !strings.Contains(doc.Content, before["knowledge/legacy-run-1.md"]) || !strings.Contains(doc.Content, "## 整理说明与来源") {
		t.Fatal("human-readable source and source hash were omitted from Markdown")
	}
	files, warnings, err := scanVault(ctx, output)
	if err != nil || len(warnings) != 0 {
		t.Fatal("organized Vault scan failed", err, warnings)
	}
	curated, notes := 0, 0
	plainReviewFound := false
	for _, file := range files {
		switch file.Document.Kind {
		case "curated":
			curated++
			candidate := curatedFromDocument(file.Document)
			if err := prepareCurated(&candidate); err != nil {
				t.Fatal("organized formal entry failed preparation", err)
			}
		case "note":
			notes++
			if file.Document.Title == "待确认的知识整理方法" {
				plainReviewFound = true
			}
		}
	}
	if curated != 1 || notes == 0 || !plainReviewFound {
		t.Fatal("unsupported/no-evidence notes were promoted or review/index notes were lost", curated, notes)
	}
	a := fixture(t, &fakeRunner{})
	warnings, imported, err := a.store.importNewCuratedFiles(ctx, files)
	if err != nil || len(warnings) != 0 || imported != 1 {
		t.Fatal("organized candidate was not imported exactly once", imported, err, warnings)
	}
	items, err := a.store.curatedKnowledgeList(ctx, "project", "375平台")
	if err != nil || len(items) != 1 {
		t.Fatal("import did not create the expected project knowledge candidate", err)
	}
	item := items[0]
	if item.Status != "observed" || item.Evidence != evidence || item.Applicability != applicability || item.SourceID != doc.SourceID || item.SourceHash != doc.SourceHash || item.SourceTaskID != doc.SourceTaskID || !reflect.DeepEqual(item.Tags, doc.Tags) {
		t.Fatal("Go import changed candidate state, evidence, tags, or provenance")
	}
	if warnings, imported, err = a.store.importNewCuratedFiles(ctx, files); err != nil || len(warnings) != 0 || imported != 0 {
		t.Fatal("repeat import duplicated candidate knowledge", imported, err, warnings)
	}
	if after := organizerSourceHashes(t, source); !reflect.DeepEqual(before, after) {
		t.Fatal("Go import changed a legacy source file")
	}
}

// The default reading layout combines literal source excerpts and navigation.
// It must remain readable without creating new formal curated identities.
// All files are synthetic; this test never opens a user database or a model.
func TestKnowledgeVaultOrganizerReadableViewParsesAndLinksWithoutCuratedImport(t *testing.T) {
	python := organizerPython(t)
	root := t.TempDir()
	source := filepath.Join(root, "legacy", "Duo")
	output := filepath.Join(root, "reading")
	report := filepath.Join(root, "report.json")
	write := func(relative, body string) {
		t.Helper()
		filename := filepath.Join(source, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"甲", "乙"} {
		write("knowledge/"+name+".md", fmt.Sprintf("---\nduo_kind: insight\nduo_task: task-375\nduo_task_title: 375调试\nduo_run: run-1\nduo_updated: 1792416579737\ntitle: 内存方法%s\ntags: [内存]\n---\n\n## 结论\n\n合成方法%s的原文。\n\n## 依据\n\n合成依据，未执行硬件验证。\n", name, name))
	}
	write("knowledge/long.md", "---\nduo_kind: insight\nduo_task: task-375\nduo_task_title: 375调试\ntitle: 内存完整长文\n---\n\n"+strings.Repeat("需要保留的合成长文。", 180)+"\n\n## 依据\n\n合成长文依据。\n")
	write("tasks/task-375/summary.md", "---\nduo_kind: task\nduo_task: task-375\nduo_task_title: 375调试\nduo_updated: 1792416579737\ntitle: 合成任务记录\n---\n\n## 当前目标\n\n合成目标。\n\n## 当前计划\n\n- [ ] 合成待执行步骤\n")
	write("tasks/task-375/conversations/run-1.md", "---\nduo_kind: run\nduo_task: task-375\nduo_task_title: 375调试\nduo_run: run-1\nduo_updated: 1792416579737\nstatus: failed\ntitle: 合成轮次\n---\n\n## 用户要求\n\n合成请求。\n\n## 回复 / 执行结果\n\n部分合成进展。\n\n## 执行错误\n\n合成超时，未完成验证。\n")
	before := organizerSourceHashes(t, source)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "scripts/Organize-KnowledgeVault.py", "--source", source, "--output", output, "--report", report, "--apply")
	hideCommand(cmd)
	if log, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("synthetic readable organizer failed: %v\n%s", err, log)
	}
	files, warnings, err := scanVault(ctx, output)
	if err != nil || len(warnings) != 0 {
		t.Fatal("readable output failed Go Markdown scanning", err, warnings)
	}
	handbooks, archives := 0, 0
	for _, file := range files {
		if file.Document.Kind == "curated" || strings.HasPrefix(file.Document.ID, "curated:") {
			t.Fatal("readable source compilation unexpectedly created formal curated identity")
		}
		if strings.Contains(file.Document.Title, "内存与性能手册") {
			handbooks++
			for _, expected := range []string{"合成方法甲的原文。", "合成方法乙的原文。", "问题导航", "关联任务过程与操作原文"} {
				if !strings.Contains(file.Document.Content, expected) {
					t.Fatalf("topic handbook lost literal content or navigation %q", expected)
				}
			}
		}
	}
	linkPattern := regexp.MustCompile(`\[[^\]]*\]\(([^\s)]+)\)`)
	err = filepath.WalkDir(output, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(filename) != ".md" {
			return walkErr
		}
		raw, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		if filepath.Base(filename) == "任务记录.md" {
			archives++
			for _, expected := range []string{"合成目标。", "合成待执行步骤", "按时间记录", "合成超时，未完成验证。"} {
				if !strings.Contains(string(raw), expected) {
					t.Fatalf("task archive lost an existing field or outcome %q", expected)
				}
			}
		}
		for _, match := range linkPattern.FindAllSubmatch(raw, -1) {
			link, err := url.Parse(string(match[1]))
			if err != nil {
				return err
			}
			if link.Scheme != "" || link.Host != "" {
				continue
			}
			path, err := url.PathUnescape(link.EscapedPath())
			if err != nil {
				return err
			}
			target := filename
			if path != "" {
				target = filepath.Clean(filepath.Join(filepath.Dir(filename), filepath.FromSlash(path)))
			}
			if _, err := os.Stat(target); err != nil {
				return fmt.Errorf("readable navigation target does not exist: %w", err)
			}
			if link.Fragment != "" && strings.HasPrefix(target, output+string(filepath.Separator)) {
				body, err := os.ReadFile(target)
				if err != nil {
					return err
				}
				if !organizerVisibleHeadingSlugs(string(body))[link.Fragment] {
					return fmt.Errorf("readable navigation anchor does not exist: %s", link.Fragment)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal("readable output contains a broken local link", err)
	}
	if handbooks != 1 || archives != 1 {
		t.Fatal("short knowledge was not combined or task archive was missing", handbooks, archives)
	}
	if !reflect.DeepEqual(before, organizerSourceHashes(t, source)) {
		t.Fatal("readable organizer modified a synthetic original")
	}
}

// Mirrors the UI's documented Markdown heading slug contract, including the
// duplicate suffix rule. Raw HTML anchors are intentionally not recognized.
func organizerVisibleHeadingSlugs(body string) map[string]bool {
	out := map[string]bool{}
	counts := map[string]int{}
	fence := byte(0)
	heading := regexp.MustCompile(`^#{1,6}[ \t]+(.+)$`)
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			if fence == 0 {
				fence = trimmed[0]
			} else if fence == trimmed[0] {
				fence = 0
			}
			continue
		}
		if fence != 0 {
			continue
		}
		match := heading.FindStringSubmatch(line)
		if len(match) != 2 {
			continue
		}
		var filtered strings.Builder
		for _, char := range strings.ToLower(strings.TrimSpace(match[1])) {
			if unicode.IsLetter(char) || unicode.IsNumber(char) || unicode.IsSpace(char) || char == '_' || char == '-' {
				filtered.WriteRune(char)
			}
		}
		base := strings.Join(strings.Fields(filtered.String()), "-")
		slug := base
		if counts[base] > 0 {
			slug = fmt.Sprintf("%s-%d", base, counts[base])
		}
		counts[base]++
		out[slug] = true
	}
	return out
}

// An opt-in audit of a separately generated real reading view. This test never
// imports, rewrites or prints user documents; only aggregate counts are logged.
func TestKnowledgeVaultOrganizerOptionalReadOnlyOutputCheck(t *testing.T) {
	root := os.Getenv("DUO_ORGANIZED_VAULT_CHECK")
	if root == "" {
		t.Skip("set DUO_ORGANIZED_VAULT_CHECK for a read-only organized-output audit")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	files, warnings, err := scanVault(ctx, root)
	if err != nil {
		t.Fatalf("organized-output read-only scan failed (%T); document details withheld", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("organized-output scan reported %d warnings; document details withheld", len(warnings))
	}
	curated, notes, other := 0, 0, 0
	for _, file := range files {
		switch file.Document.Kind {
		case "curated":
			curated++
			candidate := curatedFromDocument(file.Document)
			if err := prepareCurated(&candidate); err != nil {
				t.Errorf("curated item %d failed preparation; document details withheld", curated)
			}
			if candidate.Evidence == "" || candidate.Applicability == "" || candidate.SourceID == "" || candidate.SourceHash == "" {
				t.Errorf("curated item %d lacks required provenance or evidence; document details withheld", curated)
			}
		case "note":
			notes++
		default:
			other++
		}
	}
	t.Logf("organized-output read-only check: curated=%d note=%d other=%d total=%d", curated, notes, other, len(files))
}

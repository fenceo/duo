#!/usr/bin/env python3
"""Offline synthetic regression for the non-destructive legacy organizer."""
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("organizer", Path(__file__).with_name("Organize-KnowledgeVault.py"))
organizer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(organizer)


def fixture(path, title, kind="insight", task="375调试", body="## 结论\n\n缓存不等于泄漏。\n\n## 依据\n\n合成样本日志显示缓存归零。\n", tags=None):
    path.parent.mkdir(parents=True, exist_ok=True)
    metadata = dict(title=title, duo_kind=kind, duo_task="task-375", duo_task_title=task,
                    duo_updated="1792416579737", status="observed", tags=tags or ["内存", "Linux"])
    path.write_text(organizer.yaml_text(metadata) + "# " + title + "\n\n" + body, encoding="utf-8")


def hashes(root):
    return {p.relative_to(root).as_posix(): hashlib.sha256(p.read_bytes()).hexdigest()
            for p in root.rglob("*") if p.is_file()}


class OrganizerTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        self.source = self.root / "notes" / "Duo"
        self.source.mkdir(parents=True)
        self.output = self.root / "notes" / "整理阅读"
        self.report = self.root / "reports" / "preview.json"

    def tearDown(self):
        self.tmp.cleanup()

    def run_organizer(self, apply=False):
        return organizer.organize(self.source.parent, self.output, self.report, apply)

    def test_preview_never_writes_source_or_output(self):
        fixture(self.source / "knowledge" / "run-id.md", "逻辑缓存与物理内存")
        before = hashes(self.source)
        data = self.run_organizer()
        self.assertEqual(data["mode"], "preview")
        self.assertEqual(hashes(self.source), before)
        self.assertFalse(self.output.exists())
        self.assertEqual(data["sources"][0]["target"], "项目知识/375平台/内存与性能/逻辑缓存与物理内存.md")

    def test_dedup_preserves_every_hash_and_source(self):
        for index in (1, 2):
            fixture(self.source / "knowledge" / f"run-{index}.md", "缓存与内存",
                    body=f"## 结论\n\n缓存不等于泄漏。\n\n## 依据\n\n合成样本日志显示缓存归零。\n\n## 来源\n\n原任务第{index}轮\n")
        before = hashes(self.source)
        data = self.run_organizer(True)
        self.assertEqual(data["note_count"], 1)
        self.assertEqual(data["same_scope_duplicate_count"], 1)
        self.assertTrue(data["source_unchanged"])
        self.assertEqual(hashes(self.source), before)
        note = next((self.output / "项目知识").rglob("缓存与内存.md")).read_text(encoding="utf-8")
        for digest in before.values():
            self.assertIn(digest, note)
        self.assertIn('status: "observed"', note)
        self.assertIn('review_status: "待核对"', note)
        self.assertIn('tags: ["Linux", "内存"]', note)
        self.assertTrue((self.output / "迁移清单.json").exists())

    def test_different_projects_are_not_merged(self):
        fixture(self.source / "knowledge" / "run-1.md", "内存问题", task="375调试")
        fixture(self.source / "knowledge" / "run-2.md", "内存问题", task="385项目")
        data = self.run_organizer()
        self.assertEqual(data["note_count"], 2)

    def test_actual_title_collision_gets_stable_suffix(self):
        fixture(self.source / "knowledge" / "run-1.md", "内存问题", body="结论甲\n")
        fixture(self.source / "knowledge" / "run-2.md", "内存问题", body="结论乙\n")
        data = self.run_organizer(True)
        targets = [s["target"] for s in data["sources"]]
        self.assertEqual(len(set(targets)), 2)
        self.assertTrue(any(__import__('re').search(r"-[a-f0-9]{8}\.md$", target) for target in targets))

    def test_dialogue_is_linked_not_copied_or_invented(self):
        fixture(self.source / "knowledge" / "dummy.md", "技术知识")
        fixture(self.source / "tasks" / "task-375" / "conversations" / "run.md", "你好", kind="conversation", body="不应复制的全部聊天内容\n")
        fixture(self.source / "tasks" / "summary.md", "旧任务摘要", kind="task_summary", body="不应当成当前目标\n")
        self.run_organizer(True)
        all_text = "\n".join(p.read_text(encoding="utf-8") for p in self.output.rglob("*.md"))
        self.assertNotIn("不应复制的全部聊天内容", all_text)
        self.assertNotIn("不应当成当前目标", all_text)
        self.assertIn("历史来源，非当前任务记录", all_text)
        self.assertIn("conversations/run.md", all_text)

    def test_credentials_not_copied(self):
        fixture(self.source / "knowledge" / "secret.md", "串口连接", body="password=fixture-password-123\n")
        fixture(self.source / "knowledge" / "url-secret.md", "访问资料", body="https://example.invalid/?access_token=fixture-secret\n")
        fixture(self.source / "knowledge" / "quoted.md", "JSON连接", body='{"password": "fixture-quoted"}\n')
        fixture(self.source / "knowledge" / "github.md", "GitHub连接", body='ghp_abcdefghijklmnopqrstu\n')
        data = self.run_organizer(True)
        output = "\n".join(p.read_text(encoding="utf-8") for p in self.output.rglob("*") if p.is_file())
        self.assertNotIn("fixture-password-123", output)
        self.assertNotIn("fixture-secret", output)
        self.assertNotIn("fixture-quoted", output)
        self.assertNotIn("ghp_abcdefghijklmnopqrstu", output)
        self.assertEqual(data["counts"]["sensitive-source"], 4)
        self.assertIn("敏感资料引用", output)

    def test_nested_and_existing_output_rejected(self):
        fixture(self.source / "knowledge" / "note.md", "技术知识")
        with self.assertRaises(ValueError):
            organizer.organize(self.source, self.source / "new", self.report)
        self.output.mkdir()
        (self.output / "人工笔记.md").write_text("保留人工笔记", encoding="utf-8")
        with self.assertRaises(ValueError):
            self.run_organizer(True)
        self.assertEqual((self.output / "人工笔记.md").read_text(encoding="utf-8"), "保留人工笔记")

    def test_relative_links_retarget_imported_notes(self):
        fixture(self.source / "knowledge" / "a.md", "内存甲", body="参考[另一结论](b.md)。\n\n## 依据\n\n甲样本。\n")
        fixture(self.source / "knowledge" / "b.md", "内存乙", body="结论乙。\n\n## 依据\n\n乙样本。\n")
        self.run_organizer(True)
        target = self.output / "项目知识" / "375平台" / "内存与性能" / "内存甲.md"
        self.assertIn("%E5%86%85%E5%AD%98%E4%B9%99.md", target.read_text(encoding="utf-8"))

    def test_source_change_aborts_before_output(self):
        target = self.source / "knowledge" / "note.md"
        fixture(target, "内存问题")
        real_render = organizer.render
        def changed_source(*args):
            rendered = real_render(*args)
            target.write_text(target.read_text(encoding="utf-8") + "\n外部新增\n", encoding="utf-8")
            return rendered
        with patch.object(organizer, "render", side_effect=changed_source):
            with self.assertRaisesRegex(ValueError, "来源已变化"):
                self.run_organizer(True)
        self.assertFalse(self.output.exists())
        self.assertIn("外部新增", target.read_text(encoding="utf-8"))

    def test_duo_curated_serialization_fields_match_go_schema(self):
        evidence = "合成日志明确显示：缓存归零，RSS未立即下降。"
        fixture(self.source / "knowledge" / "run-id.md", "缓存与内存",
                body="## 结论\n\n缓存不等于泄漏。\n\n## 依据\n\n" + evidence + "\n")
        data = self.run_organizer(True)
        target = self.output / data["sources"][0]["target"]
        raw = target.read_text(encoding="utf-8")
        # The writer uses JSON scalar/list values, a strict YAML subset; parse
        # those values independently rather than coercing through its reader.
        head = raw.split("---\n", 2)[1]
        properties = {line.split(":", 1)[0]: json.loads(line.split(":", 1)[1])
                      for line in head.splitlines() if line.strip()}
        import re
        self.assertRegex(properties["duo_id"], r"^curated:[0-9a-f]{24}$")
        self.assertEqual(properties["duo_kind"], "curated")
        self.assertEqual(properties["duo_layer"], "project")
        self.assertEqual(properties["duo_scope_key"], "375平台")
        self.assertEqual(properties["duo_evidence"], evidence)
        self.assertIn("原任务历史资料", properties["duo_applicability"])
        self.assertEqual(properties["status"], "observed")
        self.assertEqual(properties["duo_revision"], 1)
        self.assertIsInstance(properties["duo_revision"], int)
        self.assertTrue(properties["duo_source_id"].startswith("external:legacy-vault/knowledge/run-id.md#"))
        self.assertEqual(data["curated_note_count"], 1)
        # Verify field names/types against the actual Go YAML serialization
        # contract. This is a schema fixture, not a claim to execute Go code.
        go_source = Path(__file__).parent.parent.joinpath("library.go").read_text(encoding="utf-8")
        go_struct = go_source.split("type LibraryDocument struct {", 1)[1].split("\n}", 1)[0]
        for field in ("duo_id", "duo_kind", "duo_layer", "duo_scope_key", "duo_evidence", "duo_applicability", "duo_source_id", "duo_source_hash", "duo_source_task"):
            self.assertRegex(go_struct, r"\bstring\s+`[^`]*yaml:\"" + field + r"(?:,omitempty)?\"")
            self.assertIsInstance(properties[field], str)
        self.assertRegex(go_struct, r"\bint64\s+`[^`]*yaml:\"duo_revision\"")

    def test_missing_evidence_stays_plain_note_and_three_layers_have_indexes(self):
        fixture(self.source / "knowledge" / "run-id.md", "缓存与内存", body="## 结论\n\n未核对的想法。\n")
        data = self.run_organizer(True)
        target = self.output / data["sources"][0]["target"]
        self.assertTrue(target.relative_to(self.output).as_posix().startswith("待整理/"))
        raw = target.read_text(encoding="utf-8")
        self.assertIn('duo_kind: "note"', raw)
        self.assertNotIn("duo_evidence:", raw)
        self.assertEqual(data["curated_note_count"], 0)
        self.assertEqual(data["plain_review_note_count"], 1)
        for directory in ("平台共识", "系统知识", "项目知识"):
            self.assertTrue((self.output / directory / "首页.md").exists())

    def test_existing_curated_block_evidence_and_scope_are_preserved(self):
        target = self.source / "system" / "old.md"
        target.parent.mkdir(parents=True)
        target.write_text('---\ntitle: 通用内存方法\nduo_kind: curated\nduo_layer: system\nduo_scope_key: Linux内存\nduo_evidence: |-\n  第一条实际依据。\n  第二条实际依据。\nduo_applicability: >-\n  仅限合成 Linux 环境，\n  不代表其他系统。\n---\n\n原正式知识正文。\n', encoding="utf-8")
        # A knowledge directory enables legacy-root auto detection, while the
        # existing system note intentionally has no task metadata.
        (self.source / "knowledge").mkdir()
        data = self.run_organizer(True)
        output = self.output / data["sources"][0]["target"]
        meta, _ = organizer.frontmatter(output.read_text(encoding="utf-8"))
        self.assertEqual(meta["duo_kind"], "curated")
        self.assertEqual(meta["duo_layer"], "system")
        self.assertEqual(meta["duo_scope_key"], "Linux内存")
        self.assertEqual(meta["duo_evidence"], "第一条实际依据。\n第二条实际依据。")
        self.assertEqual(meta["duo_applicability"], "仅限合成 Linux 环境， 不代表其他系统。")


if __name__ == "__main__":
    unittest.main(verbosity=2)

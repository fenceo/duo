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

    def run_organizer(self, apply=False, layout="source-notes"):
        return organizer.organize(self.source.parent, self.output, self.report, apply, layout)

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

    def document(self, name, body, **metadata):
        path = self.source / name
        path.parent.mkdir(parents=True, exist_ok=True)
        base = dict(duo_kind="run", duo_task="task-375", duo_task_title="375调试",
                    duo_updated="1792416579737", status="done", title="合成任务轮次")
        base.update(metadata)
        path.write_text(organizer.yaml_text(base) + body, encoding="utf-8")
        return path

    def test_readable_is_default_and_combines_short_notes_without_semantic_rewrite(self):
        for index in range(3):
            fixture(self.source / "knowledge" / f"note-{index}.md", f"内存问题{index}",
                    body=f"## 结论\n\n第{index}个原始判断，待条件复核。\n\n## 依据\n\n第{index}段原文依据。\n")
        before = hashes(self.source)
        data = organizer.organize(self.source.parent, self.output, self.report, True)
        self.assertEqual(data["layout"], "readable")
        self.assertEqual(data["note_count"], 3)
        self.assertEqual(data["short_note_count"], 3)
        self.assertEqual(data["topic_handbook_count"], 1)
        self.assertEqual(data["knowledge_document_count"], 1)
        self.assertEqual(data["curated_note_count"], 0)
        targets = {r["target"] for r in data["sources"]}
        self.assertEqual(len(targets), 1)
        text = (self.output / targets.pop()).read_text(encoding="utf-8")
        self.assertIn("问题导航", text)
        self.assertIn('duo_kind: "note"', text)
        self.assertIn("也未迁移 Duo 数据库", (self.output / "首页.md").read_text(encoding="utf-8"))
        for index in range(3):
            self.assertIn(f"第{index}个原始判断，待条件复核。", text)
            self.assertIn(f"第{index}段原文依据。", text)
        for digest in before.values():
            self.assertIn(digest, text)
        self.assertEqual(before, hashes(self.source))

    def test_readable_task_archive_expands_fields_and_results_by_stable_task_id(self):
        self.document("tasks/task-375/summary.md", "## 任务目标\n\n明确的合成验证目标。\n\n## 当前计划\n\n- [ ] 运行合成检查\n\n## 当前进展\n\n合成进展已保存。\n", duo_kind="task", duo_task_title="375新任务名")
        self.document("tasks/task-375/context.md", "## 决策与共识 · 2026-10-19\n\n只使用合成样本。\n\n## 仍需解决 · 2026-10-19\n\n硬件验证尚未执行。\n", duo_kind="task")
        self.document("tasks/task-375/conversations/run-1.md", "## 用户要求\n\n先检查样本。\n\n## 回复 / 执行结果\n\n第一轮结果：合成代码检查已通过。\n\n## 执行错误\n", duo_run="run-1", duo_task_title="375旧任务名", duo_updated="1788220800000")
        self.document("tasks/task-375/conversations/run-2.md", "## 用户要求\n\n下一步检查。\n\n## 回复 / 执行结果\n\n第二轮保留部分进展。\n\n## 执行错误\n\n合成超时，没有完成验证。\n", duo_run="run-2", status="failed")
        # A duplicate export of run-1 is one event, not a second history turn.
        self.document("archive/run-1.md", "## 用户要求\n\n先检查样本。\n\n## 回复 / 执行结果\n\n第一轮结果：合成代码检查已通过。\n", duo_kind="automatic", duo_run="run-1", duo_updated="1788220800000")
        fixture(self.source / "knowledge" / "fact.md", "内存证据")
        data = self.run_organizer(True, "readable")
        self.assertEqual(data["task_count"], 1)
        self.assertEqual(data["timeline_event_count"], 2)
        masters = list((self.output / "任务记录").rglob("任务记录.md"))
        self.assertEqual(len(masters), 1)
        main = masters[0].read_text(encoding="utf-8")
        for text in ("明确的合成验证目标。", "运行合成检查", "合成进展已保存。", "只使用合成样本。", "硬件验证尚未执行。", "合成样本日志显示缓存归零。", "按时间记录", "第一轮结果", "合成超时"):
            self.assertIn(text, main)
        logs = "\n".join(path.read_text(encoding="utf-8") for path in masters[0].parent.joinpath("时间线").glob("*.md"))
        self.assertIn("第一轮结果：合成代码检查已通过。", logs)
        self.assertIn("第二轮保留部分进展。", logs)
        self.assertIn("合成超时，没有完成验证。", logs)
        self.assertIn("failed", logs)
        self.assertIn("SHA-256", logs)

    def test_readable_missing_state_is_explicit_and_user_question_is_not_a_goal(self):
        self.document("tasks/task-375/conversations/run.md", "## 用户要求\n\n请把这个提问当成当前目标是不正确的。\n\n## 回复 / 执行结果\n\n只有来源结果，没有结构化计划。\n", duo_run="run")
        data = self.run_organizer(True, "readable")
        main = next((self.output / "任务记录").rglob("任务记录.md")).read_text(encoding="utf-8")
        goals = main.split("## 目标与验收（已有来源）", 1)[1].split("## 计划（已有来源）", 1)[0]
        self.assertIn("未找到可安全摘录的明确字段", goals)
        self.assertNotIn("请把这个提问", goals)
        self.assertEqual(data["timeline_event_count"], 1)
        logs = next((self.output / "任务记录").rglob("时间线/*.md")).read_text(encoding="utf-8")
        self.assertIn("本轮要求（非当前目标）", logs)
        self.assertIn("请把这个提问", logs)

    def test_readable_handbooks_and_long_log_pages_are_bounded_and_have_navigation(self):
        for index in range(47):
            fixture(self.source / "knowledge" / f"note-{index}.md", f"内存问题{index:02d}",
                    body=f"## 结论\n\n合成判断{index}。\n\n## 依据\n\n合成依据{index}。\n")
        for index in range(18):
            self.document(f"tasks/task-375/conversations/run-{index}.md", "## 用户要求\n\n合成要求\n\n## 回复 / 执行结果\n\n" + str(index) + "较长的合成原文。" * 600, duo_run=f"run-{index}")
        data = self.run_organizer(True, "readable")
        self.assertEqual(data["topic_handbook_count"], 3)
        self.assertGreater(data["timeline_page_count"], 1)
        for path in (self.output / "项目知识").rglob("*手册*.md"):
            text = path.read_text(encoding="utf-8")
            self.assertLess(len(text), 32000)
            self.assertIn("问题导航", text)
        for path in (self.output / "任务记录").rglob("时间线/*.md"):
            if path.name == "首页.md":
                continue
            text = path.read_text(encoding="utf-8")
            self.assertLess(len(text), 40000)
            self.assertIn("轮次导航", text)
            self.assertIn("原文较长，此处节选", text)

    def test_readable_relative_links_land_on_correct_handbook_entry(self):
        fixture(self.source / "knowledge" / "a.md", "内存甲", body="参考[乙结论](b.md)。\n\n## 依据\n\n甲样本。\n")
        fixture(self.source / "knowledge" / "b.md", "内存乙", body="乙判断。\n\n## 依据\n\n乙样本。\n")
        data = self.run_organizer(True, "readable")
        second = next(r for r in data["sources"] if r["source"].endswith("b.md"))
        text = (self.output / second["target"]).read_text(encoding="utf-8")
        self.assertIn("#" + second["target_anchor"] + ")", text)
        self.assertIn(second["target_anchor"], [organizer.heading_slug(line[3:]) for line in text.splitlines() if line.startswith("## ")])
        self.assertNotIn("<a id=", text)

    def test_readable_sensitive_conversation_is_source_only_and_legacy_curated_task_is_linked(self):
        self.document("tasks/task-375/conversations/run.md", "## 用户要求\n\n检查\n\n## 回复 / 执行结果\n\npassword=fixture-protected-value\n", duo_run="run")
        self.document("projects/375/fact.md", "原有内存结论。", title="内存方法", duo_kind="curated", duo_layer="project", duo_scope_key="375平台", duo_task="", duo_source_task="task-375", duo_task_title="", duo_evidence="合成证据。")
        data = self.run_organizer(True, "readable")
        text = "\n".join(path.read_text(encoding="utf-8") for path in self.output.rglob("*.md"))
        self.assertNotIn("fixture-protected-value", text)
        self.assertIn("本轮原文疑似含敏感信息", text)
        self.assertEqual(data["task_count"], 1)
        master = next((self.output / "任务记录").rglob("任务记录.md")).read_text(encoding="utf-8")
        self.assertIn("内存与性能手册", master)
        self.assertIn("合成证据。", master)

    def test_readable_same_task_name_does_not_merge_distinct_task_ids(self):
        for index in (1, 2):
            self.document(f"tasks/task-{index}/conversations/run.md", "## 回复 / 执行结果\n\n合成结果。\n", duo_task=f"task-{index}", duo_run=f"run-{index}")
        data = self.run_organizer(True, "readable")
        self.assertEqual(data["task_count"], 2)
        self.assertEqual(len(list((self.output / "任务记录").rglob("任务记录.md"))), 2)

    def test_readable_larger_notes_stay_complete_and_code_headings_are_not_rewritten(self):
        body = "## 结论\n\n完整长文。" + "保留上下文。" * 300 + "\n\n```sh\n## code-heading\necho sample\n```\n\n## 依据\n\n合成证据。\n"
        fixture(self.source / "knowledge" / "long.md", "内存长文", body=body)
        data = self.run_organizer(True, "readable")
        self.assertEqual(data["short_note_count"], 0)
        self.assertEqual(data["knowledge_document_count"], 1)
        output = self.output / next(r["target"] for r in data["sources"])
        self.assertIn(body, output.read_text(encoding="utf-8"))
        self.assertIn('duo_kind: "note"', output.read_text(encoding="utf-8"))
        self.assertNotIn('duo_id: "curated:', output.read_text(encoding="utf-8"))
        self.assertIn("## code-heading", organizer.nested_headings(body))

    def test_readable_curated_run_source_links_to_full_task_context(self):
        self.document("tasks/task-375/conversations/run.md", "## 回复 / 执行结果\n\n完整操作过程。\n", duo_run="run")
        self.document("projects/375/knowledge.md", "内存结论。", title="内存操作方法", duo_kind="curated", duo_layer="project", duo_scope_key="375平台", duo_task="", duo_source_task="task-375", duo_task_title="", duo_source_id="run:run", duo_evidence="来源依据。")
        data = self.run_organizer(True, "readable")
        record = next(r for r in data["sources"] if r["source"].endswith("knowledge.md"))
        self.assertEqual(record["run_id"], "run")
        handbook = (self.output / record["target"]).read_text(encoding="utf-8")
        self.assertIn("关联任务过程与操作原文", handbook)
        event = next(r for r in data["sources"] if r["source"].endswith("conversations/run.md"))
        self.assertIn(event["reading_target"].split("#", 1)[1], handbook)
        self.assertIn("完整操作过程。", (self.output / event["reading_target"].split("#", 1)[0]).read_text(encoding="utf-8"))

    def test_readable_handbook_name_collision_never_overwrites_long_note(self):
        long_body = "长知识原文。" * 300 + "\n\n## 依据\n\n合成依据。\n"
        fixture(self.source / "knowledge" / "long.md", "内存与性能手册", body=long_body)
        fixture(self.source / "knowledge" / "short.md", "内存操作方法")
        data = self.run_organizer(True, "readable")
        self.assertEqual(data["knowledge_document_count"], 2)
        targets = {r["target"] for r in data["sources"]}
        self.assertEqual(len(targets), 2)
        long_record = next(r for r in data["sources"] if r["source"].endswith("long.md"))
        self.assertIn(long_body, (self.output / long_record["target"]).read_text(encoding="utf-8"))

    def test_readable_truncated_code_fence_does_not_swallow_navigation(self):
        excerpt = organizer.clipped("开头\n```sh\n" + "echo synthetic\n" * 1000 + "```\n", 100)
        self.assertEqual(excerpt.count("```"), 2)
        self.assertIn("补齐了节选中未闭合的代码块标记", excerpt)
        self.assertIn("原文较长，此处节选", excerpt)

    def test_readable_current_progress_is_preferred_over_history_in_same_snapshot(self):
        self.document("tasks/task-375/summary.md", "## 当前进展\n\n最新状态原文。\n\n## 进展记录\n\n早期历史，不应冒充当前进展。\n", duo_kind="task")
        self.run_organizer(True, "readable")
        main = next((self.output / "任务记录").rglob("任务记录.md")).read_text(encoding="utf-8")
        progress = main.split("## 进展（已有来源）", 1)[1].split("## 决策与约定（已有来源）", 1)[0]
        self.assertIn("最新状态原文。", progress)
        self.assertNotIn("早期历史，不应冒充当前进展。", progress)

    def test_readable_duplicate_source_heading_uses_renderer_suffix_in_navigation(self):
        self.document("tasks/task-375/summary.md", "## 当前目标\n\n目标原文。\n\n### 按时间记录\n\n原文里的同名小标题。\n", duo_kind="task")
        self.run_organizer(True, "readable")
        main = next((self.output / "任务记录").rglob("任务记录.md")).read_text(encoding="utf-8")
        self.assertIn("[按时间浏览全部轮次](#按时间记录-1)", main)
        self.assertNotIn("<a id=", main)

    def test_readable_identical_long_sources_in_different_projects_keep_distinct_note_ids(self):
        body = "需要保留的相同长原文。" * 160 + "\n\n## 依据\n\n合成依据。\n"
        fixture(self.source / "knowledge" / "one.md", "内存长文", task="375调试", body=body)
        fixture(self.source / "knowledge" / "two.md", "内存长文", task="385调试", body=body)
        data = self.run_organizer(True, "readable")
        identifiers = [organizer.frontmatter((self.output / record["target"]).read_text(encoding="utf-8"))[0]["duo_id"] for record in data["sources"]]
        self.assertEqual(len(set(identifiers)), 2)

    def test_redacted_path_links_remain_visible_citations_without_fake_files(self):
        placeholders = ("<local-path>", "<workspace>/task.md", "%3Clocal-path%3E",
                        "路径已脱敏", "[本机路径已脱敏]", "脱敏")
        for placeholder in placeholders:
            with self.subTest(placeholder=placeholder):
                body = "参见[原任务记录](" + placeholder + ")。"
                actual = organizer.rewrite_links(body, self.source / "tasks" / "record.md",
                                                 self.output / "任务记录.md", self.source, self.output, {})
                self.assertEqual(actual, "参见原任务记录（原路径已脱敏，不可恢复）。")
                self.assertNotIn("%3C", actual)

    def test_rewriting_links_preserves_fenced_and_inline_code_exactly(self):
        body = ("[普通链接](target.md)\n\n"
                "`[行内代码](target.md)` 和 ``[行内含`符号](<local-path>)``\n\n"
                "```md\n[代码示例](target.md)\n[脱敏示例](<local-path>)\n```\n\n"
                "~~~~markdown\n[另一示例](target.md)\n~~~\n仍在代码块\n~~~~\n\n"
                "[脱敏引用](<workspace>/task.md)\n")
        actual = organizer.rewrite_links(body, self.source / "knowledge" / "record.md",
                                         self.output / "任务记录.md", self.source, self.output,
                                         {"knowledge/target.md": "知识手册.md#目标"})
        self.assertIn("[普通链接](%E7%9F%A5%E8%AF%86%E6%89%8B%E5%86%8C.md#目标)", actual)
        self.assertIn("脱敏引用（原路径已脱敏，不可恢复）", actual)
        for code in ("`[行内代码](target.md)`", "``[行内含`符号](<local-path>)``",
                     "```md\n[代码示例](target.md)\n[脱敏示例](<local-path>)\n```",
                     "~~~~markdown\n[另一示例](target.md)\n~~~\n仍在代码块\n~~~~"):
            self.assertIn(code, actual)

    def test_heading_navigation_preserves_links_inside_code(self):
        body = ("# 主文档\n\n### 目标\n\n## 目标\n\n"
                "[普通链接](#目标)\n\n`[行内代码](#目标)`\n\n"
                "```md\n[代码示例](#目标)\n```\n")
        files = {"主文档.md": body}
        organizer.close_heading_navigation(files, [], self.output)
        actual = files["主文档.md"]
        self.assertIn("[普通链接](#目标-1)", actual)
        self.assertIn("`[行内代码](#目标)`", actual)
        self.assertIn("```md\n[代码示例](#目标)\n```", actual)

    def test_missing_original_file_is_a_visible_path_citation_not_a_dead_link(self):
        body = ("[第2轮](conversations/task-00000001.md#验证)\n\n"
                "`[代码引用](conversations/task-00000001.md)`\n\n"
                "```md\n[示例引用](conversations/task-00000001.md)\n```\n")
        actual = organizer.rewrite_links(body, self.source / "tasks" / "task" / "summary.md",
                                         self.output / "任务记录.md", self.source, self.output, {})
        self.assertIn("第2轮（原引用文件缺失；原路径：`tasks/task/conversations/task-00000001.md#验证`）", actual)
        self.assertNotIn("[第2轮]", actual)
        self.assertIn("`[代码引用](conversations/task-00000001.md)`", actual)
        self.assertIn("```md\n[示例引用](conversations/task-00000001.md)\n```", actual)

    def test_redacted_words_in_real_anchor_or_external_url_are_not_path_placeholders(self):
        body = "[本节](#本机路径已脱敏) 和 [公开文档](https://example.test/路径已脱敏)"
        actual = organizer.rewrite_links(body, self.source / "tasks" / "summary.md",
                                         self.output / "任务记录.md", self.source, self.output, {})
        self.assertEqual(actual, body)


if __name__ == "__main__":
    unittest.main(verbosity=2)

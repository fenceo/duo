#!/usr/bin/env python3
"""Non-destructive, offline organizer for the legacy Duo Markdown export.

Preview is the default. --apply creates an entirely new output directory; it
never overwrites, removes, renames or modifies a source. No model is called.
The output is an import/review view, not independently verified knowledge.
"""
from __future__ import annotations

import argparse
from collections import Counter, defaultdict
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import sys
from urllib.parse import quote, unquote, urlsplit


VERSION = 1
DUO_LAYERS = {"平台共识": "consensus", "系统知识": "system", "项目知识": "project"}
SENSITIVE = re.compile(
    r"(?i)(?:-----BEGIN [^\r\n-]*PRIVATE KEY-----|"
    r"\bBearer\s+[A-Za-z0-9._~+/=-]{8,}|\bsk-[A-Za-z0-9_-]{12,}|"
    r"\b(?:gh[pousr]_[A-Za-z0-9_-]{12,}|github_pat_[A-Za-z0-9_]+|"
    r"glpat-[A-Za-z0-9_-]{12,}|xox[baprs]-[A-Za-z0-9-]{12,}|AKIA[0-9A-Z]{16})\b|"
    r"[\"'](?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|auth(?:orization)?|"
    r"password|secret|private[_ -]?key)[\"']\s*:\s*[\"'][^\"'\r\n]*[\"']|"
    r"\b(?:password|passwd|secret|api[_ -]?key|access[_ -]?token|"
    r"refresh[_ -]?token|token|auth(?:orization)?|bearer|private[_ -]?key)\s*[:=：]\s*\S+|"
    r"(?:密码|口令|密钥|令牌)\s*(?:是|为|[:=：])\s*\S+|"
    r"https?://[^\s/:]+:[^\s/@]+@|"
    r"https?://\S*[?&](?:token|key|password|secret|access_token)=\S+)"
)
GENERIC = re.compile(r"^(?:你好[，,！!。\s]*|仅?只?回复\s*ok|对\s*AI\s*提问的回答[：:]?|继续[。！!\s]*)$", re.I)
TOPICS = [
    ("内存与性能", r"内存|泄漏|缓存|DDR|\bkvs\b|\brss\b|\bheap\b|\bmalloc\b|性能"),
    ("启动与烧录", r"\bnfs\b|\buboot\b|u-boot|烧录|启动|bootloader"),
    ("音视频与通信", r"音频|视频|摄像|\brtsp\b|\bwebrtc\b|\bagora\b|编解码|\baac\b"),
    ("AI与执行环境", r"\bcodex\b|\bclaude\b|harness|\bmimo\b|\bwsl\b|模型|沙箱|\bacp\b|\bcli\b"),
    ("知识管理与界面", r"知识|obsidian|笔记|界面|菜单|布局|便签"),
    ("构建与发布", r"构建|发布|编译|\bCI\b|github|版本|安装包"),
    ("设备调试", r"串口|调试|硬件|驱动|\busb\b|\bjtag\b"),
]


def sha(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def within(path: Path, parent: Path) -> bool:
    try:
        path.relative_to(parent)
        return True
    except ValueError:
        return False


def scalar(value: str):
    value = value.strip()
    if value.startswith('"'):
        try:
            return json.loads(value)
        except (json.JSONDecodeError, TypeError):
            pass
    if value.startswith("'") and value.endswith("'"):
        return value[1:-1].replace("''", "'")
    if value.startswith("["):
        try:
            return json.loads(value)
        except json.JSONDecodeError:
            return [part.strip().strip("\"'") for part in value[1:-1].split(",") if part.strip()]
    return value


def frontmatter(text: str):
    """Read only scalar/list fields used by Duo; never execute YAML tags."""
    match = re.match(r"\A\ufeff?---\r?\n(.*?)\r?\n---(?:\r?\n|$)", text, re.S)
    if not match:
        return {}, text.lstrip("\ufeff")
    metadata, current = {}, None
    lines = match.group(1).splitlines()
    position = 0
    while position < len(lines):
        line = lines[position]
        field = re.match(r"^([\w-]+):\s*(.*)$", line)
        if field:
            current = field[1]
            value = field[2].strip()
            if value in ("|", "|-", "|+", ">", ">-", ">+"):
                block = []
                position += 1
                while position < len(lines) and (not lines[position].strip() or lines[position][0].isspace()):
                    block.append(lines[position])
                    position += 1
                indentation = min((len(text) - len(text.lstrip()) for text in block if text.strip()), default=0)
                pieces = [text[indentation:] if text.strip() else "" for text in block]
                if value.startswith("|"):
                    metadata[current] = "\n".join(pieces)
                else:
                    metadata[current] = "\n\n".join(" ".join(part.splitlines()) for part in "\n".join(pieces).split("\n\n"))
                if not value.endswith("-"):
                    metadata[current] += "\n"
                continue
            metadata[current] = scalar(value) if value else []
        elif current and (item := re.match(r"^\s+-\s+(.*)$", line)):
            if not isinstance(metadata[current], list):
                metadata[current] = []
            metadata[current].append(scalar(item[1]))
        elif current and line[:1].isspace() and isinstance(metadata[current], str):
            # Go's YAML serializer can fold long plain scalar strings.
            metadata[current] += " " + line.strip()
        position += 1
    return metadata, text[match.end():].lstrip("\r\n")


def safe_name(text: str, fallback: str = "待命名") -> str:
    text = str(text or "").strip()
    if SENSITIVE.search(text):
        return "含敏感信息的资料"
    text = re.sub(r"[<>:\"/\\|?*\x00-\x1f]", "-", text)
    text = re.sub(r"\s+", " ", text).strip(" .-")[:72].rstrip(" .")
    if not text:
        text = fallback
    if re.fullmatch(r"(?i)(con|prn|aux|nul|com[1-9]|lpt[1-9])", text):
        text = "资料-" + text
    return text


def tags_for(metadata: dict) -> list[str]:
    values = metadata.get("tags", [])
    if not isinstance(values, list):
        values = [values]
    return sorted(set(str(t).strip() for t in values if str(t).strip() and not SENSITIVE.search(str(t))))


def project_for(title: str) -> str:
    if re.search(r"(?i)375", title):
        return "375平台"
    if re.search(r"(?i)385", title):
        return "385平台"
    if re.search(r"(?i)\bt33\b", title):
        return "T33平台"
    if re.search(r"(?i)duo|简作", title):
        return "Duo"
    if "富瀚" in title:
        return "富瀚平台"
    if re.search(r"(?i)vega", title):
        return "Vega平台"
    return "待确认项目"


def topic_for(title: str, tags: list[str]) -> str:
    text = title + " " + " ".join(tags)
    for name, pattern in TOPICS:
        if re.search(pattern, text, re.I):
            return name
    return "其他主题"


def content_core(body: str) -> str:
    # Existing source variants are deliberately folded together; provenance is
    # kept separately for every input, including byte-level source hashes.
    body = re.split(r"(?m)^##\s+来源\s*$", body, maxsplit=1)[0]
    return "\n".join(line.rstrip() for line in body.replace("\r\n", "\n").strip().splitlines()) + "\n"


def section_text(body: str, names: tuple[str, ...]) -> str:
    """Return a literal source excerpt; never synthesize test evidence."""
    headings = "|".join(re.escape(name) for name in names)
    match = re.search(r"(?ms)^##[ \t]+(?:" + headings + r")[ \t]*\r?\n(.*?)(?=^##[ \t]+|\Z)", body)
    return match[1].strip() if match else ""


def source_scalar(metadata: dict, name: str) -> str:
    value = metadata.get(name, "")
    # Unsupported YAML structures must not become evidence strings.
    return value.strip() if isinstance(value, str) and value.strip() not in ("|", "|-", "|+", ">", ">-", ">+") else ""


def yaml_text(metadata: dict) -> str:
    rows = ["---"]
    for key, value in metadata.items():
        rows.append(key + ": " + json.dumps(value, ensure_ascii=False))
    return "\n".join(rows + ["---", ""])


def relative_link(source: Path, target: Path) -> str:
    return quote(os.path.relpath(target, source.parent).replace("\\", "/"), safe="/.-_~")


def label(text: str) -> str:
    return str(text).replace("[", "（").replace("]", "）").replace("\n", " ").replace("|", "｜")


def source_date(metadata: dict) -> str:
    value = metadata.get("duo_updated", "")
    try:
        return datetime.fromtimestamp(int(value) / 1000, timezone.utc).isoformat(timespec="seconds")
    except (ValueError, TypeError, OverflowError, OSError):
        return "日期未标注"


def discover(source: Path) -> Path:
    if (source / "Duo" / "knowledge").is_dir() and not (source / "knowledge").is_dir():
        return source / "Duo"
    return source


def scan(source: Path) -> list[dict]:
    records = []
    for path in sorted(source.rglob("*.md"), key=lambda p: p.as_posix().casefold()):
        if path.is_symlink() or not within(path.resolve(), source):
            raise ValueError("拒绝读取知识根之外的符号链接：" + str(path.relative_to(source)))
        raw = path.read_bytes()
        text = raw.decode("utf-8-sig")
        meta, body = frontmatter(text)
        rel = path.relative_to(source).as_posix()
        title = str(meta.get("title") or "")
        if not title:
            heading = re.search(r"(?m)^#\s+(.+)$", body)
            title = heading[1].strip() if heading else path.stem
        kind = str(meta.get("duo_kind") or "")
        layer = str(meta.get("duo_layer") or "")
        task_title = str(meta.get("duo_task_title") or "")
        task_id = str(meta.get("duo_task") or "")
        tags = tags_for(meta)
        parts = Path(rel).parts
        is_index = kind in ("index", "topic") or parts[0] == "topics" or path.stem.casefold() in ("readme", "首页")
        is_conversation = "conversations" in parts or kind in ("conversation", "run", "automatic") or layer == "automatic"
        is_task = parts[0] == "tasks" or kind in ("task", "task_summary", "prerequisites", "worklog")
        if is_index:
            action = "index-source"
        elif is_conversation:
            action = "conversation-source"
        elif is_task:
            action = "task-source"
        elif GENERIC.fullmatch(title.strip()) or title.startswith("对 AI 提问的回答"):
            action = "low-signal-source"
        else:
            action = "knowledge"
        core = content_core(body)
        evidence = section_text(core, ("依据", "证据", "实际依据")) or source_scalar(meta, "duo_evidence")
        applicability = section_text(core, ("适用条件", "适用范围")) or source_scalar(meta, "duo_applicability")
        original_scope = source_scalar(meta, "duo_scope_key")
        if not applicability:
            applicability = "原任务历史资料；仅限来源所述条件，待复核，不代表通用适用。"
        if re.fullmatch(r"(?:未提供|无|暂无|待验证|待核对|未验证)[。.!！\s]*", evidence):
            evidence = ""
        sensitive = bool(SENSITIVE.search(title + "\n" + task_title + "\n" + core + "\n" + evidence + "\n" + applicability + "\n" + original_scope))
        if sensitive and action == "knowledge":
            action = "sensitive-source"
        project = project_for(task_title or title)
        if layer == "project" and original_scope:
            project = safe_name(original_scope)
        # Never infer cross-task consensus or system truth merely from a tag.
        # Preserve explicit curated layer; all legacy insights remain project
        # candidates (or unclassified candidates), pending human review.
        if action == "knowledge":
            if parts[0] == "consensus" or layer == "consensus":
                category = "平台共识"
            elif parts[0] == "system" or layer == "system":
                category = "系统知识"
            else:
                category = "项目知识" if project != "待确认项目" else "待整理"
        else:
            category = "来源资料"
        review_reason = ""
        if action == "knowledge" and (not evidence or category == "待整理" or len(title) > 120 or len(original_scope) > 80 or any(c in original_scope for c in "\r\n\x00") or len((evidence + applicability).encode("utf-8")) > 20000 or len(core.encode("utf-8")) > 180000):
            review_reason = "缺少来源依据" if not evidence else "项目待确认或字段超出正式知识限制"
            category = "待整理"
        records.append(dict(source=rel, sha256=sha(raw), title=safe_name(title),
                            raw_title=title, task_title=safe_name(task_title, "未命名任务"),
                            task_id=task_id, kind=kind, layer=layer, date=source_date(meta),
                            tags=tags, project=project, topic=original_scope if category == "系统知识" and original_scope else topic_for(title, tags),
                            action=action, category=category, sensitive=sensitive,
                            evidence=evidence, applicability=applicability, review_reason=review_reason,
                            original_scope_key=original_scope,
                            original_status=str(meta.get("status") or "未标注"),
                            core=core, content_hash=sha(core.encode("utf-8"))))
    return records


def plan(records: list[dict]) -> list[dict]:
    # Distinct project contexts are not silently merged. Identical content
    # within the same scope/topic is one note with all source references.
    groups = defaultdict(list)
    for record in records:
        if record["action"] == "knowledge":
            scope = record["project"] if record["category"] in ("项目知识", "待整理") else "跨项目"
            groups[(record["category"], scope, record["topic"], record["content_hash"])].append(record)
    notes, occupied = [], {}
    for key, sources in sorted(groups.items()):
        category, project, topic, digest = key
        title = sources[0]["raw_title"]
        parents = [category]
        if category in ("项目知识", "待整理"):
            parents.append(project)
        parents.append(topic)
        target = Path(*parents) / (safe_name(title) + ".md")
        folded = target.as_posix().casefold()
        if folded in occupied and occupied[folded] != digest:
            target = target.with_stem(target.stem + "-" + digest[:8])
        occupied[target.as_posix().casefold()] = digest
        notes.append(dict(target=target.as_posix(), title=title, category=category,
                          project=project, topic=topic, sources=sources,
                          evidence=sources[0]["evidence"], applicability=sources[0]["applicability"],
                          original_scope_key=sources[0]["original_scope_key"],
                          content_hash=digest, core=sources[0]["core"]))
        for record in sources:
            record["target"] = target.as_posix()
    return notes


def manifest(records: list[dict], notes: list[dict]) -> dict:
    action_counts = Counter(r["action"] for r in records)
    return dict(format=VERSION, created_at=datetime.now(timezone.utc).isoformat(),
                mode="preview", source_count=len(records), note_count=len(notes),
                same_scope_duplicate_count=sum(len(n["sources"]) - 1 for n in notes),
                curated_note_count=sum(n["category"] in DUO_LAYERS and bool(n["evidence"]) for n in notes),
                plain_review_note_count=sum(n["category"] not in DUO_LAYERS or not n["evidence"] for n in notes),
                classification="规则建议，全部待核对；未调用模型或复验事实",
                counts=dict(sorted(action_counts.items())),
                sources=[{k: v for k, v in r.items() if k not in ("core", "raw_title", "evidence", "applicability", "original_scope_key")} for r in records])


def rewrite_links(body: str, source_file: Path, output_file: Path, source: Path, output: Path, mapping: dict) -> str:
    def replace(match):
        raw = match[2]
        parsed = urlsplit(raw)
        if parsed.scheme or raw.startswith(("#", "//")):
            return match[0]
        target = (source_file.parent / unquote(parsed.path)).resolve()
        if not within(target, source):
            return match[0]
        relative = target.relative_to(source).as_posix()
        destination = output / mapping[relative] if relative in mapping else target
        suffix = ("#" + parsed.fragment) if parsed.fragment else ""
        return "[" + match[1] + "](" + relative_link(output_file, destination) + suffix + ")"
    return re.sub(r"\[([^\]]*)\]\(([^\s)]+)\)", replace, body)


def render(records: list[dict], notes: list[dict], source: Path, output: Path, data: dict):
    files = {}
    mapping = {r["source"]: r["target"] for r in records if "target" in r}
    for note in notes:
        destination = output / note["target"]
        tags = sorted(set(t for r in note["sources"] for t in r["tags"]))
        body = rewrite_links(note["core"], source / note["sources"][0]["source"], destination, source, output, mapping)
        footer = "\n## 整理说明与来源\n\n规则归类与同范围正文去重；未调用模型，未独立复验。原文状态、适用限制以来源为准。\n\n"
        for record in sorted(note["sources"], key=lambda r: (r["date"], r["source"])):
            footer += "- [" + label(record["date"] + " · " + record["task_title"]) + "](" + relative_link(destination, source / record["source"]) + ") · SHA-256 `" + record["sha256"] + "`\n"
        is_curated = note["category"] in DUO_LAYERS and bool(note["evidence"])
        layer = DUO_LAYERS.get(note["category"], "files")
        scope = "" if layer == "consensus" else note["project"] if layer == "project" else note["original_scope_key"] or note["topic"]
        digest = sha(json.dumps([layer, scope, note["content_hash"]], ensure_ascii=False, separators=(",", ":")).encode("utf-8"))[:24]
        first = note["sources"][0]
        metadata = dict(duo_format=1, duo_id=("curated:" if is_curated else "note:") + digest,
                        duo_kind="curated" if is_curated else "note", duo_layer=layer,
                        duo_revision=1, title=note["title"], type="knowledge", project=note["project"],
                        status="observed", review_status="待核对", verification="来源回复，未独立复验", tags=tags,
                        content_sha256=note["content_hash"], source_count=len(note["sources"]))
        if is_curated:
            metadata.update(duo_scope_key=scope, duo_applicability=note["applicability"],
                            duo_evidence=note["evidence"],
                            duo_source_id="external:legacy-vault/" + first["source"] + "#" + first["sha256"],
                            duo_source_hash=first["sha256"], duo_source_task=first["task_id"])
        else:
            metadata["review_reason"] = first["review_reason"] or "待核对来源与分类"
        if len((body + footer).strip().encode("utf-8")) > 200000 and is_curated:
            raise ValueError("来源列表合并后超过正式知识200KiB限制，请拆分来源索引：" + note["target"])
        files[note["target"]] = yaml_text(metadata) + body + footer
    # Raw dialogue remains at the original location, as historical evidence.
    # Task source files are NOT promoted into current goals/plans by heuristic.
    tasks = defaultdict(list)
    for record in records:
        if record["action"] in ("task-source", "conversation-source", "low-signal-source"):
            tasks[(record["project"], record["task_id"] or "无任务标识", record["task_title"])].append(record)
    for (project, task_id, task_title), sources in sorted(tasks.items()):
        task_name = safe_name(task_title) + ("-" + safe_name(task_id)[:8] if task_id != "无任务标识" else "")
        target = Path("任务来源") / project / (task_name + ".md")
        body = yaml_text(dict(title=task_title, type="task-source-index", status="历史来源，非当前任务记录", tags=["任务来源", project]))
        body += "# " + task_title + "\n\n此页只整理原始资料的时间和来源，不把旧提问推断为目标，不捏造计划、进展或验证结果。当前任务主文档应在工作区持续维护。\n\n"
        for record in sorted(sources, key=lambda r: (r["date"], r["source"])):
            body += "- [" + label(record["date"] + " · " + record["title"]) + "](" + relative_link(output / target, source / record["source"]) + ")\n"
        files[target.as_posix()] = body
    sensitive = [r for r in records if r["action"] == "sensitive-source"]
    protected_path = Path("待整理/敏感资料引用.md")
    protected = "# 敏感资料引用\n\n以下正文疑似含有凭据，未复制到新知识目录。仅列原位置引用；请在受保护配置中维护凭据。自动检测不是完整安全审计。\n\n"
    for record in sensitive:
        protected += "- [" + label(record["title"]) + "](" + relative_link(output / protected_path, source / record["source"]) + ") · SHA-256 `" + record["sha256"] + "`\n"
    files[protected_path.as_posix()] = protected
    # Every directory gets a small navigable index, rather than one giant
    # flat list. Links, tags and note names work in ordinary Markdown too.
    directories = {Path("."), Path("平台共识"), Path("系统知识"), Path("项目知识")}
    for filename in files:
        parent = Path(filename).parent
        while parent != Path("."):
            directories.add(parent)
            parent = parent.parent
    for directory in sorted(directories, key=lambda p: p.as_posix()):
        index = directory / "首页.md"
        children = sorted(p for p in directories if p.parent == directory and p != directory)
        local_files = sorted(p for p in map(Path, files) if p.parent == directory and p.name != "首页.md")
        title = "知识整理首页" if directory == Path(".") else directory.name
        body = yaml_text(dict(title=title, type="index", status="整理预览，待核对", tags=["知识索引"])) + "# " + title + "\n\n"
        if directory == Path("."):
            body += "这是非破坏整理的独立阅读目录，未替换 Duo 设置或任何原文件。历史对话只保留来源链接，知识正文按规则归类且全部待核对。\n\n"
            body += f"扫描 {len(records)} 份原文；整理 {len(notes)} 篇知识；合并 {data['same_scope_duplicate_count']} 份同范围正文副本。全部来源与原始 SHA-256 见 [迁移清单](迁移清单.json)。\n\n"
        if directory == Path("平台共识"):
            body += "只收录用户明确认可、可跨任务使用的约定。本轮规则整理不会把普通助手建议提升为平台共识；暂无条目时保留本入口。\n\n"
        elif directory == Path("系统知识"):
            body += "收录已有明确来源的通用原理、方法和学习总结。项目实验结论不会仅凭标签自动成为通用规律；暂无条目时保留本入口。\n\n"
        elif directory == Path("项目知识"):
            body += "按项目和主题组织来源明确的知识候选。所有导入条目仍待验证，来源和适用条件需要分别核对。\n\n"
        for child in children:
            body += "- [" + label(child.name) + "](" + relative_link(output / index, output / child / "首页.md") + ")\n"
        for filename in local_files:
            body += "- [" + label(filename.stem) + "](" + relative_link(output / index, output / filename) + ")\n"
        files[index.as_posix()] = body
    return files


def organize(source_arg: Path, output: Path, report: Path, apply: bool = False) -> dict:
    selected_root = source_arg.resolve(strict=True)
    source = discover(selected_root).resolve(strict=True)
    output = output.resolve()
    report = report.resolve()
    if not source.is_dir() or source.is_symlink():
        raise ValueError("来源必须是普通目录")
    if output == source or within(output, source) or within(source, output):
        raise ValueError("输出目录必须独立于原件目录，不能相互包含")
    if within(report, source) or within(report, output):
        raise ValueError("预览报告必须放在原件和输出目录之外")
    if output.exists():
        raise ValueError("输出目录已存在；为避免覆盖，请选择新的独立目录")
    if report.exists():
        raise ValueError("报告已存在；为避免覆盖，请选择新的报告文件")
    records = scan(source)
    notes = plan(records)
    data = manifest(records, notes)
    files = render(records, notes, source, output, data)
    data["output_markdown_count"] = len(files)
    data["outputs"] = sorted(files)
    # The user-visible manifest uses relative source paths, never copied
    # bodies, secrets, or machine-specific absolute paths.
    report.parent.mkdir(parents=True, exist_ok=True)
    with report.open("x", encoding="utf-8", newline="\n") as handle:
        handle.write(json.dumps(data, ensure_ascii=False, indent=2) + "\n")
    if not apply:
        return data
    expected_sources = {record["source"] for record in records}
    current_sources = {path.relative_to(source).as_posix() for path in source.rglob("*.md")}
    if current_sources != expected_sources:
        raise ValueError("来源文件集合已变化，必须重新预览后再整理")
    for record in records:
        if sha((source / record["source"]).read_bytes()) != record["sha256"]:
            raise ValueError("来源已变化，必须重新预览后再整理：" + record["source"])
    output.mkdir(parents=True, exist_ok=False)
    for filename, content in files.items():
        target = output / filename
        if not within(target.resolve(), output):
            raise ValueError("输出路径超出整理目录")
        target.parent.mkdir(parents=True, exist_ok=True)
        with target.open("x", encoding="utf-8", newline="\n") as handle:
            handle.write(content)
    # A final read verifies every source is unchanged after all new writes.
    for record in records:
        if sha((source / record["source"]).read_bytes()) != record["sha256"]:
            raise ValueError("整理过程中来源变化；输出尚未标记完成：" + record["source"])
    if {path.relative_to(source).as_posix() for path in source.rglob("*.md")} != expected_sources:
        raise ValueError("整理过程中来源文件集合变化；输出尚未标记完成")
    completed = dict(data, mode="applied", source_unchanged=True)
    with (output / "迁移清单.json").open("x", encoding="utf-8", newline="\n") as handle:
        handle.write(json.dumps(completed, ensure_ascii=False, indent=2) + "\n")
    return completed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True, help="选定知识根或旧 Duo 目录")
    parser.add_argument("--output", type=Path, required=True, help="独立、尚不存在的新阅读目录")
    parser.add_argument("--report", type=Path, required=True, help="原件/输出之外的新 JSON 预览文件")
    parser.add_argument("--apply", action="store_true", help="创建新阅读目录；原件始终不变")
    args = parser.parse_args()
    try:
        data = organize(args.source, args.output, args.report, args.apply)
    except (OSError, UnicodeError, ValueError) as error:
        print("整理未完成：" + str(error), file=sys.stderr)
        return 1
    print(json.dumps({k: data[k] for k in ("mode", "source_count", "note_count", "curated_note_count", "plain_review_note_count", "same_scope_duplicate_count", "output_markdown_count", "counts")}, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    sys.stdout.reconfigure(encoding="utf-8")
    sys.stderr.reconfigure(encoding="utf-8")
    raise SystemExit(main())

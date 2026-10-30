#!/usr/bin/env python3
"""Non-destructive, offline organizer for the legacy Duo Markdown export.

Preview is the default. --apply creates an entirely new output directory; it
never overwrites, removes, renames or modifies a source. No model is called.
The output is an independent reading/review view, not a database migration or
independently verified knowledge. The readable layout groups short knowledge
by topic and expands historical task records from literal source sections.
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
import unicodedata
from urllib.parse import quote, unquote, urlsplit


VERSION = 2
SHORT_NOTE_CHARS = 1200
HANDBOOK_PAGE_CHARS = 22000
HANDBOOK_PAGE_ENTRIES = 20
LOG_PAGE_CHARS = 32000
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


def heading_slug(title: str) -> str:
    """Match Duo's visible Markdown heading anchor algorithm (no raw HTML)."""
    kept = "".join(char for char in title.lower().strip()
                   if unicodedata.category(char).startswith(("L", "N")) or char.isspace() or char in "_-")
    return "-".join(kept.split())


def source_date(metadata: dict) -> str:
    value = metadata.get("duo_updated", "")
    try:
        return datetime.fromtimestamp(int(value) / 1000, timezone.utc).isoformat(timespec="seconds")
    except (ValueError, TypeError, OverflowError, OSError):
        return "日期未标注"


def discover(source: Path) -> Path:
    known = ("knowledge", "tasks", "projects", "system", "consensus")
    if any((source / "Duo" / folder).is_dir() for folder in known) and not any((source / folder).is_dir() for folder in known):
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
        task_id = str(meta.get("duo_task") or meta.get("duo_source_task") or "")
        run_id = str(meta.get("duo_run") or "")
        if not run_id and str(meta.get("duo_source_id") or "").startswith("run:"):
            run_id = str(meta["duo_source_id"])[4:]
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
                            task_id=task_id, run_id=run_id, kind=kind, layer=layer, date=source_date(meta),
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


def transform_markdown_prose(body: str, transform) -> str:
    """Transform prose without touching fenced blocks or inline code spans."""
    def prose(text):
        chunks, position, search = [], 0, 0
        while opening := re.search(r"`+", text[search:]):
            start, end = search + opening.start(), search + opening.end()
            # An escaped backtick does not open an inline code span. Inside a
            # code span, however, backslashes are literal and do not escape it.
            backslashes, before = 0, start - 1
            while before >= 0 and text[before] == "\\":
                backslashes += 1
                before -= 1
            if backslashes % 2:
                search = end
                continue
            closing = re.search(r"(?<!`)`{" + str(end - start) + r"}(?!`)", text[end:])
            if not closing:
                search = end
                continue
            stop = end + closing.end()
            chunks.extend((transform(text[position:start]), text[start:stop]))
            position = search = stop
        chunks.append(transform(text[position:]))
        return "".join(chunks)

    chunks, pending, fence = [], [], ""
    for line in body.splitlines(keepends=True):
        marker = re.match(r"^ {0,3}(`{3,}|~{3,})([^\r\n]*)", line)
        if fence:
            chunks.append(line)
            if marker and marker[1][0] == fence[0] and len(marker[1]) >= len(fence) and not marker[2].strip():
                fence = ""
        elif marker and (marker[1][0] != "`" or "`" not in marker[2]):
            chunks.append(prose("".join(pending)))
            pending = []
            chunks.append(line)
            fence = marker[1]
        else:
            pending.append(line)
    chunks.append(prose("".join(pending)))
    return "".join(chunks)


def rewrite_links(body: str, source_file: Path, output_file: Path, source: Path, output: Path, mapping: dict) -> str:
    def replace(match):
        raw = match[2]
        parsed = urlsplit(raw[1:-1] if raw.startswith("<") and raw.endswith(">") else raw)
        if parsed.scheme or raw.startswith(("#", "//")):
            return match[0]
        decoded = unquote(raw)
        if re.search(r"(?i)<(?:local[-_]path|workspace)>|(?:本[机地])?路径已脱敏|^(?:[<\[【])?(?:已)?脱敏(?:[>\]】])?$", decoded):
            # Sanitized history contains placeholders, not recoverable source
            # paths. Keep a visible citation instead of inventing a file URL.
            return match[1] + "（原路径已脱敏，不可恢复）"
        target = (source_file.parent / unquote(parsed.path)).resolve()
        if not within(target, source):
            return match[0]
        relative = target.relative_to(source).as_posix()
        mapped = mapping.get(relative, "")
        if not mapped and not target.exists():
            original = relative + ("#" + parsed.fragment if parsed.fragment else "")
            delimiter = "`" * (max((len(run) for run in re.findall(r"`+", original)), default=0) + 1)
            return match[1] + "（原引用文件缺失；原路径：" + delimiter + original + delimiter + "）"
        mapped_path, _, mapped_anchor = mapped.partition("#")
        destination = output / mapped_path if mapped else target
        fragment = mapped_anchor or parsed.fragment
        suffix = ("#" + fragment) if fragment else ""
        return "[" + match[1] + "](" + relative_link(output_file, destination) + suffix + ")"
    return transform_markdown_prose(body, lambda text: re.sub(r"\[([^\]]*)\]\(([^\s)]+)\)", replace, text))


def render(records: list[dict], notes: list[dict], source: Path, output: Path, data: dict):
    if data.get("layout") == "readable":
        return render_readable(records, notes, source, output, data)
    files = {}
    mapping = {r["source"]: r["target"] + ("#" + r["target_anchor"] if r.get("target_anchor") else "") for r in records if "target" in r}
    for note in notes:
        destination = output / note["target"]
        tags = sorted(set(t for r in note["sources"] for t in r["tags"]))
        body = rewrite_links(note["core"], source / note["sources"][0]["source"], destination, source, output, mapping)
        footer = "\n## 整理说明与来源\n\n规则归类与同范围正文去重；未调用模型，未独立复验。原文状态、适用限制以来源为准。\n\n"
        for record in sorted(note["sources"], key=lambda r: (r["date"], r["source"])):
            footer += "- [" + label(record["date"] + " · " + record["task_title"]) + "](" + relative_link(destination, source / record["source"]) + ") · SHA-256 `" + record["sha256"] + "`\n"
        reading_view = data.get("read_only_view", False)
        if reading_view:
            if note["evidence"] and not section_text(note["core"], ("依据", "证据", "实际依据")):
                footer += "\n## 来源依据（未独立复验）\n\n" + note["evidence"] + "\n"
            if not section_text(note["core"], ("适用条件", "适用范围")):
                footer += "\n## 适用边界（来源字段）\n\n" + note["applicability"] + "\n"
        is_curated = note["category"] in DUO_LAYERS and bool(note["evidence"]) and not reading_view
        layer = "files" if reading_view else DUO_LAYERS.get(note["category"], "files")
        scope = "" if layer == "consensus" else note["project"] if layer == "project" else note["original_scope_key"] or note["topic"]
        identity = ["reading", note["target"], note["content_hash"]] if reading_view else [layer, scope, note["content_hash"]]
        digest = sha(json.dumps(identity, ensure_ascii=False, separators=(",", ":")).encode("utf-8"))[:24]
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


def clipped(text: str, limit: int) -> str:
    """A marked literal excerpt, never an inferred summary or completion."""
    truncated = len(text) > limit
    visible = text[:limit].strip()
    fence = ""
    for line in visible.splitlines():
        marker = re.match(r"^\s*(`{3,}|~{3,})", line)
        if marker:
            if not fence:
                fence = marker[1]
            elif marker[1][0] == fence[0] and len(marker[1]) >= len(fence):
                fence = ""
    if fence:
        visible += "\n" + fence + "\n\n> 为保持阅读导航，补齐了节选中未闭合的代码块标记；原件未改。"
    if truncated:
        visible += "\n\n> 原文较长，此处节选；未展示部分请打开来源。"
    return visible


def nested_headings(text: str, minimum: int = 4) -> str:
    """Keep code samples intact while nesting source headings under an entry."""
    lines, fence = [], ""
    for line in text.splitlines():
        marker = re.match(r"^\s*(`{3,}|~{3,})", line)
        if marker:
            if not fence:
                fence = marker[1][0]
            elif marker[1][0] == fence:
                fence = ""
        if not fence and (heading := re.match(r"^(#{1,6})\s+(.+)$", line)):
            line = "#" * max(minimum, len(heading[1])) + " " + heading[2]
        lines.append(line)
    return "\n".join(lines)


def source_reference(record: dict, destination: Path, source: Path) -> str:
    return ("[" + label(record["date"] + " · " + record["title"]) + "](" +
            relative_link(destination, source / record["source"]) + ") · SHA-256 `" + record["sha256"] + "`")


def literal_sections(body: str) -> list[tuple[str, str]]:
    """Top-level source sections, excluding headings inside fenced code."""
    headings, fence = [], ""
    lines = body.splitlines()
    for position, line in enumerate(lines):
        marker = re.match(r"^\s*(`{3,}|~{3,})", line)
        if marker:
            if not fence:
                fence = marker[1][0]
            elif marker[1][0] == fence:
                fence = ""
            continue
        if not fence and (heading := re.match(r"^##\s+(.+?)\s*$", line)):
            headings.append((position, heading[1]))
    return [(title, "\n".join(lines[position + 1:headings[index + 1][0] if index + 1 < len(headings) else len(lines)]).strip())
            for index, (position, title) in enumerate(headings)]


TASK_FIELDS = {
    "目标与验收": ("当前目标与验收标准", "当前目标", "目标与范围", "任务目标", "验收标准"),
    "计划": ("当前计划", "计划", "任务计划"),
    "进展": ("当前进展与结论", "当前进展", "进展", "进展记录"),
    "决策与约定": ("决策与共识", "已确认决策及原因", "决策", "已确认决策"),
    "证据与验证": ("证据与验证", "依据", "证据", "实际验证结果", "实际验证"),
    "未解决事项": ("仍需解决", "未解决事项", "未解决问题", "阻塞、未解决问题与下一步", "阻塞与下一步"),
    "继续任务的前提": ("继续任务的前提", "前提条件"),
}


def field_excerpts(records: list[dict], field: str) -> list[tuple[dict, str, str]]:
    found = []
    for record in records:
        if record["sensitive"] or record["action"] != "task-source":
            continue
        for title, content in literal_sections(record["core"]):
            bare = title.split(" · ", 1)[0].strip()
            if bare in TASK_FIELDS[field] and content and not SENSITIVE.search(content):
                found.append((record, title, content))
    # Context snapshots put newest dated blocks first, but their file update
    # time is shared. The section date is part of the ordering/provenance.
    return sorted(found, key=lambda item: (item[0]["date"], -TASK_FIELDS[field].index(item[1].split(" · ", 1)[0].strip()), item[1]), reverse=True)


def conversation_fields(record: dict) -> tuple[str, str, str]:
    if record["sensitive"]:
        return "", "", ""
    body = record["core"]
    request = section_text(body, ("用户要求", "本轮要求"))
    start = re.search(r"(?m)^##\s+(?:回复 / 执行结果|最终回复)\s*$", body)
    if start:
        remainder = body[start.end():]
        response = re.split(r"(?m)^##\s+执行错误\s*$", remainder, maxsplit=1)[0].strip()
    else:
        response = ""
    error = section_text(body, ("执行错误",))
    return request, response, error


def concise_excerpt(text: str, limit: int = 160) -> str:
    # A display excerpt, not a model-generated summary. Keep source wording.
    text = re.sub(r"\[([^\]]+)\]\([^)]*\)", r"\1", text)
    text = re.sub(r"[`#*_>|]", "", text)
    text = " ".join(text.split())
    return text[:limit] + ("…" if len(text) > limit else "")


def reading_groups(notes: list[dict]) -> tuple[list[dict], list[dict]]:
    groups, standalone = defaultdict(list), []
    for note in notes:
        if len(note["core"]) <= SHORT_NOTE_CHARS:
            groups[(note["category"], note["project"], note["topic"])].append(note)
        else:
            standalone.append(note)
    books = []
    occupied = {note["target"].casefold() for note in standalone}
    for (category, project, topic), entries in sorted(groups.items()):
        parents = [category]
        if category in ("项目知识", "待整理"):
            parents.append(project)
        parents.append(safe_name(topic))
        pages, current, size = [], [], 0
        for entry in sorted(entries, key=lambda n: (n["title"].casefold(), n["content_hash"])):
            estimate = len(entry["core"]) + len(entry["evidence"]) + len(entry["applicability"]) + 300 * len(entry["sources"]) + 500
            if current and (len(current) >= HANDBOOK_PAGE_ENTRIES or size + estimate > HANDBOOK_PAGE_CHARS):
                pages.append(current)
                current, size = [], 0
            current.append(entry)
            size += estimate
        if current:
            pages.append(current)
        for number, page in enumerate(pages, 1):
            title = topic + "手册" + (f"-{number:02d}" if number > 1 else "")
            target = Path(*parents) / (safe_name(title) + ".md")
            if target.as_posix().casefold() in occupied:
                target = target.with_stem(target.stem + "-汇编-" + sha(repr((category, project, topic, number)).encode())[:8])
            occupied.add(target.as_posix().casefold())
            book = dict(target=target.as_posix(), title=title, entries=page,
                        category=category, project=project, topic=topic, page=number, pages=len(pages))
            books.append(book)
            for entry_number, entry in enumerate(page, 1):
                entry["reading_anchor"] = heading_slug(str(entry_number) + ". " + label(entry["title"]))
                for record in entry["sources"]:
                    record["target"] = target.as_posix()
                    record["target_anchor"] = entry["reading_anchor"]
    return books, standalone


def task_groups(records: list[dict]) -> list[dict]:
    groups = defaultdict(list)
    for record in records:
        if record["task_id"]:
            groups[record["task_id"]].append(record)
        elif record["action"] in ("task-source", "conversation-source", "low-signal-source"):
            groups["title:" + record["project"] + ":" + record["task_title"]].append(record)
    tasks = []
    for identity, sources in sorted(groups.items()):
        named = [r for r in sources if r["task_title"] != "未命名任务"]
        primary = max(named or sources, key=lambda r: (r["date"], r["source"]))
        title = primary["task_title"]
        if title == "未命名任务":
            title = "任务资料-" + sha(identity.encode())[:8]
        project = primary["project"]
        target = Path("任务记录") / safe_name(project) / (safe_name(title) + "-" + sha(identity.encode())[:8]) / "任务记录.md"
        # Several export views of one run become one chronological event.
        grouped_events = defaultdict(list)
        for record in sources:
            if record["action"] == "conversation-source":
                grouped_events[record["run_id"] or record["source"]].append(record)
        events = []
        for run_id, variants in grouped_events.items():
            preferred = max(variants, key=lambda r: (bool(conversation_fields(r)[1]), r["kind"] == "run", len(r["core"])))
            request, response, error = conversation_fields(preferred)
            event = dict(record=preferred, sources=variants, request=request, response=response, error=error,
                         marker="轮次-" + sha((identity + ":" + run_id).encode())[:8])
            event["title"] = concise_excerpt(request or preferred["title"], 100)
            event["heading"] = label(preferred["date"] + " · " + event["title"] + " · " + event["marker"])
            event["anchor"] = heading_slug(event["heading"])
            event["summary"] = concise_excerpt(error or response or "无可复制的结果正文；请核对来源。")
            events.append(event)
        events.sort(key=lambda event: (event["record"]["date"], event["record"]["source"]))
        tasks.append(dict(id=identity, title=title, project=project, target=target.as_posix(), sources=sources, events=events))
    return tasks


def task_pages(task: dict):
    months = defaultdict(list)
    for event in task["events"]:
        date = event["record"]["date"]
        months[date[:7] if re.match(r"^\d{4}-\d{2}", date) else "日期未标注"].append(event)
    pages = []
    for month, events in sorted(months.items()):
        # A month may itself contain hundreds of runs. Only the detailed
        # excerpts are paginated; the master table still lists every event.
        chunks, current, size = [], [], 0
        for event in events:
            estimate = min(len(event["request"]), 600) + min(len(event["response"]), 3600) + min(len(event["error"]), 1800) + 900
            if current and size + estimate > LOG_PAGE_CHARS:
                chunks.append(current)
                current, size = [], 0
            current.append(event)
            size += estimate
        if current:
            chunks.append(current)
        for number, chunk in enumerate(chunks, 1):
            filename = month + (f"-第{number:02d}页" if number > 1 else "") + ".md"
            target = Path(task["target"]).parent / "时间线" / filename
            pages.append((target, month, chunk))
            for event in chunk:
                event["target"] = target.as_posix()
                for record in event["sources"]:
                    record["reading_target"] = target.as_posix() + "#" + event["anchor"]
    return months, pages


def readable_indexes(files: dict[str, str], output: Path, data: dict):
    directories = {Path("."), Path("平台共识"), Path("系统知识"), Path("项目知识"), Path("任务记录")}
    for filename in files:
        parent = Path(filename).parent
        while parent != Path("."):
            directories.add(parent)
            parent = parent.parent
    for directory in sorted(directories, key=lambda p: p.as_posix()):
        index = directory / "首页.md"
        children = sorted(p for p in directories if p.parent == directory and p != directory)
        local = sorted(p for p in map(Path, files) if p.parent == directory and p.name != "首页.md")
        title = "任务与知识阅读首页" if directory == Path(".") else directory.name
        body = yaml_text(dict(title=title, type="index", status="历史资料整理，待核对", tags=["阅读索引"])) + "# " + title + "\n\n"
        if directory == Path("."):
            body += ("这是独立阅读整理视图，未修改原件、未调用模型、未重做实验，也未迁移 Duo 数据库。条目身份已重建；不要直接切换已有数据库的知识根并期待无重复。\n\n"
                     "先从 **任务记录** 查目标、计划、结果和按月时间线；从 **项目知识/系统知识/平台共识** 阅读主题手册。手册按来源分节保留结论和证据，不声称冲突已解决或补齐了原文没有的知识。\n\n"
                     f"原始来源 {data['source_count']} 份；去重后知识片段 {data['note_count']} 条，组合为 {data['knowledge_document_count']} 份阅读文档；任务 {data['task_count']} 个，轮次事件 {data['timeline_event_count']} 个。完整路径和 SHA-256 见 [整理清单](迁移清单.json)。\n\n")
        elif directory == Path("任务记录"):
            body += "按稳定任务标识合并历史资料；任务页展开已有目标、计划、决策和证据，并提供可浏览的逐月结果时间线。缺失字段会明确说明，历史来源不等于当前状态。\n\n"
        elif directory == Path("平台共识"):
            body += "仅保留原有明确分类，不把普通助手建议提升为用户约定。暂无资料时保留此入口。\n\n"
        elif directory == Path("系统知识"):
            body += "仅保留原有明确系统分类；不把某项目的实验结论推广为通用原理。\n\n"
        elif directory == Path("项目知识"):
            body += "按项目和主题合编短知识；每个来源片段保留依据、边界和原件链接，尚未做语义重写或事实复验。\n\n"
        for child in children:
            body += "- [" + label(child.name) + "](" + relative_link(output / index, output / child / "首页.md") + ")\n"
        for path in local:
            body += "- [" + label(path.stem) + "](" + relative_link(output / index, output / path) + ")\n"
        files[index.as_posix()] = body


def close_heading_navigation(files: dict[str, str], records: list[dict], output: Path):
    """Resolve repeated visible heading slugs after literal excerpts are added.

    Source headings also consume renderer slug counters. Generated destination
    headings are level two; literal excerpt headings are nested below them.
    """
    destinations = {}
    for filename, body in files.items():
        counts, fence = Counter(), ""
        for line in body.splitlines():
            marker = re.match(r"^\s*(`{3,}|~{3,})", line)
            if marker:
                if not fence:
                    fence = marker[1][0]
                elif marker[1][0] == fence:
                    fence = ""
                continue
            heading = re.match(r"^(#{1,6})\s+(.+)$", line) if not fence else None
            if heading:
                base = heading_slug(heading[2])
                slug = base + ("-" + str(counts[base]) if counts[base] else "")
                counts[base] += 1
                if len(heading[1]) == 2:
                    destinations.setdefault((filename, base), slug)
    def adjusted(filename, raw):
        parsed = urlsplit(raw)
        if parsed.scheme or parsed.netloc or not parsed.fragment:
            return raw
        target = (output / Path(filename).parent / unquote(parsed.path)).resolve() if parsed.path else (output / filename).resolve()
        if not within(target, output):
            return raw
        relative = target.relative_to(output).as_posix()
        original = unquote(parsed.fragment)
        slug = destinations.get((relative, original), original)
        return raw.rsplit("#", 1)[0] + "#" + slug
    for filename, body in files.items():
        files[filename] = transform_markdown_prose(body, lambda text: re.sub(
            r"\[([^\]]*)\]\(([^\s)]+)\)",
            lambda match: "[" + match[1] + "](" + adjusted(filename, match[2]) + ")", text))
    for record in records:
        if record.get("target_anchor"):
            record["target_anchor"] = destinations.get((record["target"], record["target_anchor"]), record["target_anchor"])
        if record.get("reading_target"):
            target, _, anchor = record["reading_target"].partition("#")
            record["reading_target"] = target + "#" + destinations.get((target, anchor), anchor)


def render_readable(records: list[dict], notes: list[dict], source: Path, output: Path, data: dict):
    books, standalone = reading_groups(notes)
    tasks = task_groups(records)
    run_context = {}
    for task in tasks:
        task["months"], task["month_pages"] = task_pages(task)
        for event in task["events"]:
            for record in event["sources"]:
                if record["run_id"]:
                    run_context[(record["task_id"], record["run_id"])] = record["reading_target"]
    # Reuse the original literal-note renderer only for longer source notes
    # and sensitive-source references. Replace its link-only task indexes.
    files = render(records, standalone, source, output, dict(data, layout="source-notes", read_only_view=True))
    files = {name: text for name, text in files.items() if not name.startswith("任务来源/") and Path(name).name != "首页.md"}
    mapping = {r["source"]: r["target"] + ("#" + r["target_anchor"] if r.get("target_anchor") else "") for r in records if "target" in r}
    for book in books:
        destination = output / book["target"]
        entries = book["entries"]
        body = yaml_text(dict(duo_format=1, duo_kind="note", duo_layer="files", duo_id="note:" + sha(book["target"].encode())[:24],
                              title=book["title"], type="topic-handbook", status="observed", review_status="待核对",
                              project=book["project"], topic=book["topic"], tags=sorted(set(t for n in entries for r in n["sources"] for t in r["tags"]))))
        body += "# " + book["title"] + "\n\n"
        body += "[返回主题目录](" + relative_link(destination, destination.parent / "首页.md") + ")\n\n"
        body += (f"第 {book['page']}/{book['pages']} 页，{len(entries)} 条来源片段。短知识按同一层级、项目和主题合编；正文逐字保留，只调整标题层级和链接。"
                 "这是一份有目录的来源汇编，不代表已完成语义整合、独立验证或正式数据库迁移。不同结论按来源并列，仍需复核。\n\n## 问题导航\n\n")
        for entry in entries:
            body += "- [" + label(entry["title"]) + "](#" + entry["reading_anchor"] + ")\n"
        for number, entry in enumerate(entries, 1):
            body += "\n## " + str(number) + ". " + label(entry["title"]) + "\n\n"
            first = entry["sources"][0]
            core = rewrite_links(entry["core"], source / first["source"], destination, source, output, mapping)
            body += nested_headings(core) + "\n\n"
            if not section_text(entry["core"], ("适用条件", "适用范围")):
                body += "### 适用边界（来源字段）\n\n" + entry["applicability"] + "\n\n"
            if entry["evidence"] and not section_text(entry["core"], ("依据", "证据", "实际依据")):
                body += "### 来源依据（未独立复验）\n\n" + entry["evidence"] + "\n\n"
            if not entry["evidence"]:
                body += "> 来源未提供可识别的依据，保留为待整理片段。\n\n"
            contexts = sorted(set(run_context[(r["task_id"], r["run_id"])] for r in entry["sources"] if (r["task_id"], r["run_id"]) in run_context))
            if contexts:
                body += "### 关联任务过程与操作原文\n\n"
                for context in contexts:
                    path, _, anchor = context.partition("#")
                    body += "- [查看该轮要求、执行结果与失败信息](" + relative_link(destination, output / path) + "#" + anchor + ")\n"
            body += "### 原始来源\n\n"
            for record in sorted(entry["sources"], key=lambda r: (r["date"], r["source"])):
                body += "- " + source_reference(record, destination, source) + "\n"
        files[book["target"]] = body
    log_pages = sum(len(task["month_pages"]) for task in tasks)
    for task in tasks:
        destination = output / task["target"]
        months, month_pages = task["months"], task["month_pages"]
        body = yaml_text(dict(title=task["title"], type="historical-task-record", task_id=task["id"], project=task["project"], status="历史来源，非当前任务记录", tags=["任务记录", task["project"]]))
        body += "# " + task["title"] + "\n\n"
        body += ("这是按同一任务标识重排的历史任务档案。目标、计划和决策只摘录原有明确小节；结果属于来源报告，未重新调查、未独立验证，不能据此确认当前状态。"
                 "各段保留时间和原件链接；原文较长时明确节选。\n\n")
        body += "## 阅读导航\n\n- [按时间浏览全部轮次](#" + heading_slug("按时间记录") + ")\n- [来源清单](#" + heading_slug("来源清单") + ")\n"
        for number, field in enumerate(TASK_FIELDS):
            body += "- [" + field + "](#" + heading_slug(field + "（已有来源）") + ")\n"
        for target, month, chunk in month_pages:
            body += "- [" + label(target.stem) + " · " + str(len(chunk)) + " 轮](" + relative_link(destination, output / target) + ")\n"
        for number, field in enumerate(TASK_FIELDS):
            body += "\n## " + field + "（已有来源）\n\n"
            excerpts = field_excerpts(task["sources"], field)
            limit = 1 if field in ("目标与验收", "计划", "进展") else 3
            if not excerpts and field == "证据与验证":
                for record in sorted(task["sources"], key=lambda r: r["date"], reverse=True):
                    if record["action"] == "knowledge" and record["evidence"] and not record["sensitive"]:
                        excerpts.append((record, "知识片段中的来源依据", record["evidence"]))
            for record, title, content in excerpts[:limit]:
                excerpt = rewrite_links(clipped(content, 2400), source / record["source"], destination, source, output, mapping)
                body += "### " + label(title) + "\n\n" + nested_headings(excerpt) + "\n\n"
                if field == "目标与验收" and "摘自最早一轮用户要求" in content:
                    body += "> 旧导出注明该字段摘自首轮要求，不代表持续维护后的当前目标。\n\n"
                body += "来源：" + source_reference(record, destination, source) + "\n\n"
            if not excerpts:
                body += "未找到可安全摘录的明确字段；未根据提问推断，也未自动补写。请查阅时间线和来源后补充。\n"
        related = sorted(set(r["target"] for r in task["sources"] if r["action"] == "knowledge" and "target" in r))
        body += "\n## 相关知识阅读\n\n"
        for target in related:
            body += "- [" + label(Path(target).stem) + "](" + relative_link(destination, output / target) + ")\n"
        if not related:
            body += "未找到带同一任务标识的知识片段；未按相似名称强行关联。\n"
        body += "\n## 按时间记录\n\n以下为结果原文的短摘录；完整节选见每轮记录，原件可由来源打开。\n\n"
        for month, events in sorted(months.items()):
            body += "### " + month + "\n\n| 时间（原时区） | 本轮要求/状态 | 结果原文摘录 | 过程记录 |\n| --- | --- | --- | --- |\n"
            for event in events:
                record = event["record"]
                link = relative_link(destination, output / event["target"]) + "#" + event["anchor"]
                body += "| " + label(record["date"]) + " | " + label(event["title"] + " · " + record["original_status"]) + " | " + label(event["summary"]) + " | [查看](" + link + ") |\n"
        if not task["events"]:
            body += "未找到逐轮对话文件；仅保留已有任务资料及知识来源。\n"
        body += "\n## 来源清单\n\n"
        body += (f"本任务关联 {len(task['sources'])} 份来源。每轮过程页和知识章节各自保留原文链接及 SHA-256；全部文件映射见 [整理清单](" +
                 relative_link(destination, output / "迁移清单.json") + ")。此处只列任务主资料，避免在正文重复堆积全部来源。\n\n")
        for record in sorted((r for r in task["sources"] if r["action"] == "task-source"), key=lambda r: (r["date"], r["source"])):
            body += "- " + source_reference(record, destination, source) + "\n"
        files[task["target"]] = body
        for target, month, chunk in month_pages:
            location = output / target
            log = "# " + task["title"] + " · " + target.stem + "\n\n[返回任务档案](" + relative_link(location, destination) + ")\n\n历史原文节选，未新增验证；来源缺失的目标、计划和决策不会自动补写。\n\n## 轮次导航\n\n"
            for event in chunk:
                log += "- [" + label(event["record"]["date"] + " · " + event["title"]) + "](#" + event["anchor"] + ")\n"
            for event in chunk:
                record = event["record"]
                log += "\n## " + event["heading"] + "\n\n状态（来源字段）：`" + label(record["original_status"]) + "`\n\n"
                for heading, text, size in (("本轮要求（非当前目标）", event["request"], 600), ("进展与执行结果（来源报告）", event["response"], 3600), ("失败/中断信息", event["error"], 1800)):
                    if text:
                        excerpt = rewrite_links(clipped(text, size), source / record["source"], location, source, output, mapping)
                        log += "### " + heading + "\n\n" + nested_headings(excerpt) + "\n\n"
                if record["sensitive"]:
                    log += "> 本轮原文疑似含敏感信息，未复制正文；请在受保护环境核对原件。\n\n"
                elif not event["response"] and not event["error"]:
                    log += "> 未找到可识别的结果或错误字段；未编造进展。\n\n"
                log += "### 原始来源\n\n"
                for variant in event["sources"]:
                    log += "- " + source_reference(variant, location, source) + "\n"
            files[target.as_posix()] = log
    data.update(short_note_count=sum(len(book["entries"]) for book in books), topic_handbook_count=len(books),
                knowledge_document_count=len(books) + len(standalone), task_count=len(tasks),
                timeline_event_count=sum(len(task["events"]) for task in tasks), timeline_page_count=log_pages,
                curated_note_count=0, plain_review_note_count=len(books) + len(standalone),
                identity_policy="独立阅读视图；新身份，不接管原数据库身份、来源或修订历史")
    readable_indexes(files, output, data)
    close_heading_navigation(files, records, output)
    return files


def organize(source_arg: Path, output: Path, report: Path, apply: bool = False, layout: str = "readable") -> dict:
    if layout not in ("readable", "source-notes"):
        raise ValueError("未知输出布局")
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
    data["layout"] = layout
    files = render(records, notes, source, output, data)
    data["output_markdown_count"] = len(files)
    data["outputs"] = sorted(files)
    # Rendering assigns handbook anchors and task timeline destinations.
    data["sources"] = [{k: v for k, v in record.items() if k not in ("core", "raw_title", "evidence", "applicability", "original_scope_key")} for record in records]
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
    parser.add_argument("--layout", choices=("readable", "source-notes"), default="readable", help="默认按任务展开时间线、按主题合编短知识；source-notes 保留旧逐条布局")
    args = parser.parse_args()
    try:
        data = organize(args.source, args.output, args.report, args.apply, args.layout)
    except (OSError, UnicodeError, ValueError) as error:
        print("整理未完成：" + str(error), file=sys.stderr)
        return 1
    keys = ("mode", "layout", "source_count", "note_count", "knowledge_document_count", "topic_handbook_count", "task_count", "timeline_event_count", "timeline_page_count", "curated_note_count", "plain_review_note_count", "same_scope_duplicate_count", "output_markdown_count", "counts")
    print(json.dumps({key: data[key] for key in keys if key in data}, ensure_ascii=False, indent=2))
    return 0


if __name__ == "__main__":
    sys.stdout.reconfigure(encoding="utf-8")
    sys.stderr.reconfigure(encoding="utf-8")
    raise SystemExit(main())

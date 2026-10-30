import hashlib
import json
import os
import re
import secrets
import sys

# This program runs inside the selected WSL/SSH execution environment. There
# is deliberately no fallback to the Windows service's filesystem.
LIMIT = 1024 * 1024
TASK_ID = re.compile(r"^[A-Za-z0-9_-]{1,96}$")
MARKER = "\n<!-- duo-workspace-generated:"
TASK_MARKER = "<!-- duo-workspace-task:"
WORKLOG_NAME = "任务记录.md"
PAGE_TARGET = 256 * 1024
WORKLOG_MONTH = re.compile(r"^(?:[0-9]{4}-(?:0[1-9]|1[0-2])|undated)$")
WORKLOG_PAGE = re.compile(r"^([0-9]{4}-(?:0[1-9]|1[0-2])|undated)-([0-9]{4})\.md$")
NAV_START = "<!-- duo:history-navigation:"
NAV_END = "<!-- /duo:history-navigation -->"
WORKLOG_ID = re.compile(r"^[A-Za-z0-9_-]{1,96}/[A-Za-z0-9_-]{1,96}$")
WORKLOG_SOURCE = re.compile(r"(?m)^<!-- duo:worklog:([A-Za-z0-9_-]{1,96}/[A-Za-z0-9_-]{1,96}) -->\r?$")
WORKLOG_DETAIL = re.compile(r"(?m)^<!-- duo:worklog-detail:([A-Za-z0-9_-]{1,96}/[A-Za-z0-9_-]{1,96}) -->\r?$")
STATE_START = "<!-- duo:task-state:"
STATE_END = "<!-- /duo:task-state -->"
CONFLICT_MESSAGE = "任务记录已被修改，请重新读取后合并；当前编辑未覆盖"
WORKLOG_NOTES = "## 备注\n\n自动管理区域维护当前状态；人工编辑该区域会暂停自动替换。区域外正文保持原样，时间记录只追加新轮次。请保留来源标记以避免重复回填。\n\n## 进展记录\n\n以下按轮次时间持续追加本轮需求、助手结论、执行摘录及结构化进展。原始需求不自动作为任务目标，历史回复和自动整理不等于独立验证；失败记录保留失败状态。\n"


class DocumentConflict(ValueError):
    pass


class MemoryFileTooLarge(ValueError):
    pass


def digest(text):
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def managed(body):
    body = body.rstrip("\n") + "\n"
    return body + MARKER + digest(body) + " -->\n"


def unmodified(text):
    start = text.rfind(MARKER)
    return start >= 0 and text.endswith(" -->\n") and text[start + len(MARKER):-5] == digest(text[:start])


def state_block(body):
    body = body.replace("\r\n", "\n").rstrip("\n") + "\n"
    return STATE_START + digest(body) + " -->\n" + body + STATE_END


def update_worklog_state(text, body):
    if not body:
        return text, False
    if not isinstance(body, str) or "\x00" in body or len(body.encode("utf-8")) > LIMIT:
        raise ValueError("任务状态内容无效或超过 1 MiB")
    start = text.find(STATE_START)
    if start < 0:
        if STATE_END in text:
            return text, True
        block = state_block(body) + "\n\n"
        separator = "\n\n"
        if "\r\n" in text:
            block = block.replace("\n", "\r\n")
            separator = "\r\n\r\n"
        at = text.find("\n## ")
        return (text[:at+1] + block + text[at+1:] if at >= 0 else text + separator + block), False
    if text.count(STATE_START) != 1 or text.count(STATE_END) != 1:
        return text, True
    header_end, end = text.find(" -->", start), text.find(STATE_END, start)
    if header_end < 0 or end < header_end:
        return text, True
    revision = text[start+len(STATE_START):header_end]
    stored = text[header_end+len(" -->"):end].replace("\r\n", "\n")
    if stored.startswith("\n"):
        stored = stored[1:]
    if not re.fullmatch(r"[0-9a-f]{64}", revision) or digest(stored) != revision:
        return text, True
    block = state_block(body)
    if "\r\n" in text[start:end]:
        block = block.replace("\n", "\r\n")
    return text[:start] + block + text[end+len(STATE_END):], False


def upgrade_legacy_worklog_header(text):
    if text.count(STATE_START) != text.count(STATE_END) or text.count(STATE_START) > 1:
        return text, False
    newline = "\r\n" if "\r\n" in text else "\n"
    if not text.startswith("---"+newline+"type: task_worklog"+newline+"task_id: "):
        return text, False
    for greeting in ("你好", "您好", "hello", "Hello", "hi", "Hi", "你好，继续", "你好,继续", "好的", "继续"):
        legacy = "## 任务目标\n\n"+greeting+"\n\n## 当前计划\n\n尚未手工填写。可在这里维护下一步计划。\n\n## 备注\n\n可直接编辑目标、计划、备注和已有记录；后续只追加新轮次。请保留来源标记以避免重复回填。\n\n## 进展记录\n\n以下按轮次时间持续追加。历史回复和自动整理不等于独立验证；失败记录保留失败状态。\n"
        expected = legacy.replace("\n", newline)
        start = text.find(newline+"## 任务目标"+newline)
        if start < 0:
            continue
        start += len(newline)
        if "<!-- duo:worklog:" in text[:start]:
            continue
        if STATE_START in text[:start] and STATE_END not in text[:start]:
            continue
        if digest(text[start:start+len(expected)]) == digest(expected):
            return text[:start]+WORKLOG_NOTES.replace("\n", newline)+text[start+len(expected):], True
    return text, False


def valid_directory(task_id, directory):
    parts = directory.split("/")
    if not isinstance(task_id, str) or not TASK_ID.fullmatch(task_id) or len(parts) != 3 or parts[:2] != [".duo", "tasks"]:
        return False
    name = parts[2]
    if name == task_id:
        return True
    suffix = "-" + digest(task_id)[:10]
    title = name[:-len(suffix)]
    return bool(title) and len(name) <= 64 and name.endswith(suffix) and all(c.isalpha() or c.isdecimal() or c in "_-" for c in title)


def valid_document(relative):
    parts = relative.split("/")
    if relative == ".duo/README.md":
        return True
    if len(parts) not in (4, 5) or parts[:2] != [".duo", "tasks"] or not parts[2] or len(parts[2]) > 96 or not all(c.isalpha() or c.isdecimal() or c in "_-" for c in parts[2]):
        return False
    return (len(parts) == 4 and parts[3] in ("README.md", "handoff.md", WORKLOG_NAME)) or (len(parts) == 5 and parts[3] == "runs" and parts[4].endswith(".md") and bool(TASK_ID.fullmatch(parts[4][:-3]))) or (len(parts) == 5 and parts[3] == "history" and (parts[4] == "README.md" or WORKLOG_PAGE.fullmatch(parts[4]) or parts[4].endswith(".md") and WORKLOG_MONTH.fullmatch(parts[4][:-3])))


def update_worklog_navigation(text, body):
    if NAV_START not in text:
        if NAV_END in text:
            return text, True
        block = state_block(body).replace(STATE_START, NAV_START).replace(STATE_END, NAV_END)
        separator = "\n\n"
        if "\r\n" in text:
            block = block.replace("\n", "\r\n")
            separator = "\r\n\r\n"
        at = text.find(STATE_END)
        if at >= 0:
            at += len(STATE_END)
            return text[:at] + separator + block + text[at:], False
        return text + separator + block + separator, False
    placeholder_start, placeholder_end = "<!-- duo-navigation-placeholder-start:", "<!-- duo-navigation-placeholder-end -->"
    if placeholder_start in text or placeholder_end in text:
        return text, True
    text = text.replace(STATE_START, placeholder_start).replace(STATE_END, placeholder_end)
    updated, conflict = update_worklog_state(text.replace(NAV_START, STATE_START).replace(NAV_END, STATE_END), body)
    return updated.replace(STATE_START, NAV_START).replace(STATE_END, NAV_END).replace(placeholder_start, STATE_START).replace(placeholder_end, STATE_END), conflict


def document_link(source, target):
    import posixpath
    return posixpath.relpath(target, posixpath.dirname(source))


def main():
    request = json.load(sys.stdin)
    root = request["root"]
    if not os.path.isabs(root) or os.path.realpath(root) != os.path.abspath(root):
        raise ValueError("工作区记忆需要无符号链接的绝对工作区路径")
    report = {"enabled": True, "directory": ".duo", "task_directory": request.get("task_directory", ""), "exported": 0,
              "unchanged": 0, "tasks": [], "conflicts": [], "warnings": [], "documents": [], "task_directories": [],
              "main_document": "", "revision": "", "editable": False}
    root_fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)

    def parent(relative, create=False):
        parts = relative.split("/")
        if parts[0] != ".duo" or len(parts) < 2 or any(
                not part or part in (".", "..") or part.lower() == ".git" or "\\" in part or
                ":" in part or "\x00" in part or part.rstrip(" .") != part for part in parts):
            raise ValueError("工作区记忆路径无效")
        fd = os.dup(root_fd)
        try:
            for part in parts[:-1]:
                if create:
                    try:
                        os.mkdir(part, 0o700, dir_fd=fd)
                    except FileExistsError:
                        pass
                child = os.open(part, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=fd)
                os.close(fd)
                fd = child
            return fd, parts[-1]
        except Exception:
            os.close(fd)
            raise

    def read(relative):
        fd, name = parent(relative)
        try:
            source_fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=fd)
            with os.fdopen(source_fd, "rb") as source:
                stat = os.fstat(source.fileno())
                import stat as stat_module
                if not stat_module.S_ISREG(stat.st_mode):
                    raise ValueError("工作区记忆不是普通文件")
                if stat.st_size > LIMIT:
                    raise MemoryFileTooLarge("工作区记忆超过 1 MiB")
                raw = source.read(LIMIT + 1)
                if len(raw) > LIMIT:
                    raise MemoryFileTooLarge("工作区记忆超过 1 MiB")
                text = raw.decode("utf-8")
                if "\x00" in text:
                    raise ValueError("工作区记忆必须是无 NUL 的 UTF-8 文本")
                return text
        finally:
            os.close(fd)

    def write(relative, text, create_only=False):
        if "\x00" in text:
            raise ValueError("工作区记忆必须是无 NUL 的 UTF-8 文本")
        encoded = text.encode("utf-8")
        if len(encoded) > LIMIT:
            raise ValueError("工作区记忆单文件超过 1 MiB")
        fd, name = parent(relative, True)
        os.close(fd)
        old = None
        try:
            old = read(relative)
        except FileNotFoundError:
            pass
        if old is not None and create_only:
            report["unchanged"] += 1
            return
        if old == text:
            report["unchanged"] += 1
            return
        if old is not None and not unmodified(old):
            relative = ".duo/conflicts/" + relative[5:].replace("/", "_") + "-" + digest(text)[:16] + ".md"
            report["conflicts"].append(relative)
            fd, name = parent(relative, True)
            os.close(fd)

            try:
                conflict = read(relative)
                if conflict == text:
                    report["unchanged"] += 1
                    return
                raise ValueError("冲突副本也已被编辑，保留双方并停止覆盖")
            except FileNotFoundError:
                old = None
        fd, name = parent(relative)
        temporary = ".duo-" + secrets.token_hex(12) + ".tmp"
        try:
            out_fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=fd)
            with os.fdopen(out_fd, "wb") as output:
                output.write(encoded)
                output.flush()
                os.fsync(output.fileno())
            try:
                current = read(relative)
            except FileNotFoundError:
                current = None
            if current != old:
                raise ValueError("写入前文档发生外部修改；已保留原文件，请重新同步")
            os.replace(temporary, name, src_dir_fd=fd, dst_dir_fd=fd)
            report["exported"] += 1
        finally:
            try:
                os.unlink(temporary, dir_fd=fd)
            except FileNotFoundError:
                pass
            os.close(fd)

    def scan_worklog_markers(relative):
        fd, name = parent(relative)
        try:
            source_fd = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=fd)
            with os.fdopen(source_fd, "rb") as source:
                import stat as stat_module
                if not stat_module.S_ISREG(os.fstat(source.fileno()).st_mode):
                    raise ValueError("工作区记忆不是普通文件")
                seen, detailed, skip = set(), set(), False
                while True:
                    line = source.readline(4096)
                    if not line:
                        return seen, detailed
                    if not line.endswith(b"\n") and len(line) >= 4096:
                        skip = True
                        continue
                    if not skip and len(line) <= 512:
                        try:
                            decoded = line.decode("ascii")
                        except UnicodeDecodeError:
                            decoded = ""
                        seen.update(WORKLOG_SOURCE.findall(decoded))
                        detailed.update(WORKLOG_DETAIL.findall(decoded))
                    skip = False
        finally:
            os.close(fd)

    def entries(relative):
        fd, _ = parent(relative + "/_")
        try:
            return os.listdir(fd)
        finally:
            os.close(fd)

    def discover_task_directory():
        task_id = request.get("task_id", "")
        requested = request.get("task_directory", "")
        if not valid_directory(task_id, requested):
            return ""
        try:
            names = entries(".duo/tasks")[:500]
        except FileNotFoundError:
            return requested
        matches = []
        for name in names:
            directory = ".duo/tasks/" + name
            if not valid_directory(task_id, directory):
                continue
            try:
                directory_fd, _ = parent(directory + "/_")
                os.close(directory_fd)
            except (OSError, ValueError):
                continue
            matches.append(directory)
        # Prefer an existing named archive to a legacy redirect or a newly
        # proposed name after the task was renamed. Every candidate remains
        # confined to this task's stable ID/hash.
        named = [item for item in matches if item.rsplit("/", 1)[-1] != task_id]
        if requested in named:
            return requested
        if named:
            return sorted(named)[0]
        if requested in matches:
            return requested
        return sorted(matches)[0] if matches else requested

    def plain_write(relative, text, old):
        if "\x00" in text:
            raise ValueError("任务记录必须是无 NUL 的 UTF-8 文本")
        encoded = text.encode("utf-8")
        if len(encoded) > LIMIT:
            raise MemoryFileTooLarge("任务记录单文件超过 1 MiB；未写入，原文件保留")
        if text == old:
            report["unchanged"] += 1
            return
        fd, name = parent(relative, True)
        temporary = ".duo-" + secrets.token_hex(12) + ".tmp"
        try:
            out_fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=fd)
            with os.fdopen(out_fd, "wb") as output:
                output.write(encoded)
                output.flush()
                os.fsync(output.fileno())
            try:
                current = read(relative)
            except FileNotFoundError:
                current = None
            if current != old:
                raise DocumentConflict(CONFLICT_MESSAGE)
            os.replace(temporary, name, src_dir_fd=fd, dst_dir_fd=fd)
            report["exported"] += 1
        finally:
            try:
                os.unlink(temporary, dir_fd=fd)
            except FileNotFoundError:
                pass
            os.close(fd)

    def append_worklog(file):
        relative = file.get("path", "")
        directory = relative.rsplit("/", 1)[0]
        if directory != report["task_directory"] or not valid_directory(request.get("task_id", ""), directory) or relative != directory + "/" + WORKLOG_NAME:
            raise ValueError("只能追加当前任务目录内的任务记录.md")
        old, oversized = None, False
        try:
            old = read(relative)
        except FileNotFoundError:
            pass
        except MemoryFileTooLarge:
            oversized = True
            report["warnings"].append("任务记录超过 1 MiB；保留人工原文，新轮次仍写入历史分页，请从任务来源索引打开历史记录。")
        text = old if old is not None else file.get("content", "")
        source_entries = file.get("entries", [])
        if not isinstance(text, str) or not isinstance(source_entries, list) or len(source_entries) > 2000:
            raise ValueError("任务记录内容格式无效或来源超过 2000 条")
        if oversized:
            text = ""
        else:
            text, upgraded = upgrade_legacy_worklog_header(text)
            if upgraded:
                report["warnings"].append("已按已知自动模板修正旧版问候目标；历史记录和人工内容保留，当前目标仍需有效任务状态确认。")
        seen, detailed = set(WORKLOG_SOURCE.findall(text)), set(WORKLOG_DETAIL.findall(text))
        if oversized:
            seen, detailed = scan_worklog_markers(relative)
        try:
            history_names = entries(directory + "/history")
        except FileNotFoundError:
            history_names = []
        pages = {}
        for name in history_names:
            match = WORKLOG_PAGE.fullmatch(name)
            if match and match[2] != "0000":
                pages.setdefault(match[1], []).append(name)
        touched = set()
        for entry in source_entries:
            identity, content, month = entry.get("id", ""), entry.get("content", ""), entry.get("month", "") or "undated"
            if not isinstance(identity, str) or not WORKLOG_ID.fullmatch(identity) or not isinstance(content, str) or not isinstance(month, str) or not WORKLOG_MONTH.fullmatch(month) or not isinstance(entry.get("details", False), bool):
                raise ValueError("任务记录来源或月份无效")
            source = entry.get("source", "")
            if source and (not isinstance(source, str) or not valid_document(source) or "/runs/" not in source):
                raise ValueError("任务来源链接无效")
            touched.add(month)
        contents, originals, blocked = {}, {}, set()
        for month in pages:
            for name in sorted(pages.get(month, [])):
                page_path = directory + "/history/" + name
                try:
                    raw = read(page_path)
                except MemoryFileTooLarge:
                    markers, details = scan_worklog_markers(page_path)
                    seen.update(markers)
                    detailed.update(details)
                    blocked.add(name)
                    report["warnings"].append("历史分页超过 1 MiB，保留原文；后续记录使用新分页：" + name)
                    continue
                if month in touched:
                    contents[name] = originals[name] = raw
                seen.update(WORKLOG_SOURCE.findall(raw))
                detailed.update(WORKLOG_DETAIL.findall(raw))
        for entry in source_entries:
            identity, content, details = entry["id"], entry["content"], entry.get("details", False)
            if identity in seen and (not details or identity in detailed):
                continue
            prefix = "<!-- duo:worklog:" + identity + " -->\n"
            if details:
                prefix += "<!-- duo:worklog-detail:" + identity + " -->\n"
                if identity in seen:
                    prefix = "<!-- duo:worklog-detail:" + identity + " -->\n\n> 历史轮次资料补全：补充原需求、回复与执行摘录；此前记录和人工编辑保持原样。\n\n"
            addition = "\n\n" + prefix + content.strip() + "\n"
            if not oversized and len((text + addition).encode("utf-8")) <= PAGE_TARGET:
                text += addition
            else:
                month = entry.get("month", "") or "undated"
                month_pages = pages.setdefault(month, [])
                month_pages.sort()
                name = month_pages[-1] if month_pages else ""
                if not name or name in blocked or len((contents[name] + addition).encode("utf-8")) > PAGE_TARGET:
                    number = int(WORKLOG_PAGE.fullmatch(name)[2]) + 1 if name else 1
                    if number > 9999:
                        raise ValueError("任务单月历史已达到 9999 分页；保留原文，未推进同步检查点")
                    name = f"{month}-{number:04d}.md"
                    month_pages.append(name)
                    contents[name] = "# " + month + " · 任务进展记录\n\n[返回任务当前状态](../" + WORKLOG_NAME + ") · [历史月份索引](README.md)\n\n按来源追加保存任务需求、结论、决策与证据；历史回填保留原发生时间，不推断为已验证知识。\n"
                if entry.get("source"):
                    source = entry["source"]
                    addition = addition.replace("](" + document_link(relative, source) + ")", "](" + document_link(directory + "/history/" + name, source) + ")", 1)
                if len((contents[name] + addition).encode("utf-8")) > LIMIT:
                    raise ValueError("单条任务记录超过历史分页上限；保留原文，未推进同步检查点")
                contents[name] += addition
            seen.add(identity)
            if details:
                detailed.add(identity)
        for name in sorted(contents):
            if contents[name] != originals.get(name):
                plain_write(directory + "/history/" + name, contents[name], originals.get(name))

        def history_index(target, body):
            try:
                before = read(target)
            except FileNotFoundError:
                before = None
            if before is not None and not unmodified(before):
                report["warnings"].append("历史索引已被人工修改，保留原文：" + target)
                return
            plain_write(target, managed(body), before)

        want_navigation = bool(pages or file.get("history_pending") or NAV_START in text)
        if want_navigation:
            index = "# 任务历史记录\n\n[返回任务当前状态](../" + WORKLOG_NAME + ")\n\n按发生月份查找；每页保留来源与执行状态，人工修改不被重写。\n\n"
            for month in sorted(pages, reverse=True):
                index += f"- [{month}]({month}.md) ? {len(pages[month])} ?\n"
                if month in touched:
                    month_index = "# " + month + " · 任务记录分页\n\n[全部月份](README.md) · [任务当前状态](../" + WORKLOG_NAME + ")\n\n"
                    month_index += "".join("- [" + name + "](" + name + ")\n" for name in sorted(pages[month]))
                    history_index(directory + "/history/" + month + ".md", month_index)
            history_index(directory + "/history/README.md", index)
        if oversized:
            return
        if file.get("state_incomplete", False):
            report["warnings"].append("任务状态历史不完整，已保留自动状态区；新进展仍写入时间记录。")
        else:
            text, conflict = update_worklog_state(text, file.get("state_content", ""))
            if conflict:
                report["warnings"].append("任务状态自动管理区域已被人工修改或标记不完整，已保留原文；新进展仍追加到时间记录。")
        if want_navigation:
            body = "## 历史与最近进展\n\n[按月份查看完整任务记录](history/README.md) · [完整轮次来源](README.md)\n\n主文档保留当前状态与既有人工内容；新增长历史自动分页，不会在达到 1 MiB 后停止记录。\n"
            recent_entries = file.get("recent_entries") or source_entries
            for entry in recent_entries[-4:]:
                raw = entry["content"].strip()
                excerpt = raw if len(raw) <= 700 else raw[:466] + "\n\n…（中间内容省略，保留开头与结尾；请打开本轮来源记录查看）…\n\n" + raw[-234:]
                body += "\n" + excerpt + "\n"
            text, conflict = update_worklog_navigation(text, body)
            if conflict:
                report["warnings"].append("历史导航区域已被人工修改，已保留原文；分页可从任务来源索引打开。")
        if len(text.encode("utf-8")) > LIMIT:
            report["warnings"].append("更新任务状态或导航将超过 1 MiB，保留主文档；新轮次已保存到历史分页。")
            return
        plain_write(relative, text, old)

    def migrate(item):
        old, new, task_id = item["from"], item["to"], item["id"]
        if old != ".duo/tasks/" + task_id or old == new or not valid_directory(task_id, new):
            raise ValueError("任务记录迁移路径无效")
        parent_fd, old_name = parent(old)
        try:
            try:
                os.stat(new.split("/")[-1], dir_fd=parent_fd, follow_symlinks=False)
                report["warnings"].append("任务目录已存在，保留原路径：" + old)
                return
            except FileNotFoundError:
                pass
            owned = []
            safe = True
            try:
                for name in entries(old):
                    if name == "runs":
                        runs = entries(old + "/runs")
                        if len(runs) > 2001:
                            safe = False
                            break
                        for run in runs:
                            if not run.endswith(".md") or not TASK_ID.fullmatch(run[:-3]):
                                safe = False
                                break
                            owned.append("runs/" + run)
                    elif name in ("README.md", "handoff.md"):
                        owned.append(name)
                    else:
                        safe = False
                        break
                total = 0
                for name in owned:
                    raw = read(old + "/" + name)
                    total += len(raw.encode("utf-8"))
                    if not unmodified(raw) or total > 32 * LIMIT:
                        safe = False
                        break
            except (OSError, ValueError):
                safe = False
            if not safe:
                report["warnings"].append("旧任务目录含人工修改、额外文件或超限资料，保留原路径：" + old)
                return
            os.rename(old_name, new.split("/")[-1], src_dir_fd=parent_fd, dst_dir_fd=parent_fd)
            for name in owned:
                source, target = old + "/" + name, new + "/" + name
                text = managed("# 任务记录已迁移\n\n[打开按任务名称归档的记录](" + document_link(source, target) + ")\n\n<!-- duo-workspace-redirect:" + new + " -->")
                write(source, text)
        finally:
            os.close(parent_fd)

    try:
        for item in request.get("migrations", []):
            migrate(item)
        owned_directory = discover_task_directory()
        if owned_directory:
            report["task_directory"] = owned_directory
            report["main_document"] = owned_directory + "/" + WORKLOG_NAME
        if request["action"] == "sync":
            for file in request.get("files", []):
                if file.get("append_worklog"):
                    append_worklog(file)
                else:
                    write(file["path"], file["content"], file.get("create_only", False))
        if request["action"] == "save_document":
            relative, text, revision = request.get("path", ""), request.get("content"), request.get("revision", "")
            if not owned_directory or relative != report["main_document"]:
                raise ValueError("只能编辑当前任务目录内的任务记录.md")
            if not isinstance(text, str):
                raise ValueError("任务记录内容格式无效")
            if not isinstance(revision, str) or not re.fullmatch(r"[0-9a-f]{64}", revision):
                raise DocumentConflict(CONFLICT_MESSAGE)
            try:
                old = read(relative)
            except FileNotFoundError:
                raise DocumentConflict(CONFLICT_MESSAGE)
            if digest(old) != revision:
                raise DocumentConflict(CONFLICT_MESSAGE)
            plain_write(relative, text, old)
            report.update({"path": relative, "content": text, "revision": digest(text), "editable": True})
            return report
        if request["action"] == "document":
            relative = request.get("path", "")
            if not valid_document(relative):
                raise ValueError("只能读取工作区内的任务 Markdown 记录")
            if relative.rsplit("/", 1)[-1] == WORKLOG_NAME and (not owned_directory or relative != report["main_document"]):
                raise ValueError("只能查看或编辑本任务的主 Markdown 记录")
            report["path"], report["content"] = relative, read(relative)
            report["revision"] = digest(report["content"])
            report["editable"] = bool(owned_directory and relative == report["main_document"])
            return report
        try:
            fd, name = parent(".duo/tasks/_")
        except FileNotFoundError:
            return report
        try:
            names = os.listdir(fd)
        finally:
            os.close(fd)
        if len(names) > 500:
            report["warnings"].append("工作区超过 500 个任务目录，本次仅发现前 500 个。")
        for name in names[:500]:
            try:
                directory = ".duo/tasks/" + name
                if valid_document(directory + "/README.md"):
                    report["task_directories"].append(directory)
                if not owned_directory and valid_directory(request.get("task_id", ""), directory) and (report["task_directory"] == request.get("task_directory") or name != request.get("task_id")):
                    report["task_directory"] = directory
                raw = read(".duo/tasks/" + name + "/handoff.md")
                end = raw.index(" -->\n")
                if not raw.startswith(TASK_MARKER):
                    continue
                task = json.loads(raw[len(TASK_MARKER):end])
                task_id = task.get("id", "")
                old_path = task.get("path", "")
                old_base = old_path.rsplit("/", 1)[0]
                directory = ".duo/tasks/" + name
                if not valid_directory(task_id, directory) or not old_path.endswith("/README.md") or not valid_directory(task_id, old_base) or len(task.get("sources", [])) > 6:
                    continue
                task["path"] = directory + "/README.md"
                if task.get("state"):
                    task["state"] = ""
                    try:
                        current = read(directory + "/" + WORKLOG_NAME)
                        start = current.find(STATE_START)
                        header = current.find(" -->", start) if start >= 0 else -1
                        state_end = current.find(STATE_END, start) if start >= 0 else -1
                        if header >= 0 and state_end > header:
                            task["state"] = current[header+len(" -->"):state_end].strip()[:2400]
                    except (OSError, ValueError):
                        pass
                for source in task.get("sources", []):
                    if source.get("path", "").startswith(old_base + "/"):
                        source["path"] = directory + source["path"][len(old_base):]
                if not unmodified(raw):
                    task["sync_cursor"] = 0
                    task["sync_cursor_run_id"] = ""
                    task["sync_pending_rows"] = []
                    task["sync_pending"] = True
                    task["excerpt"] = raw[end + 5:][:2400]
                    task["sources"] = [{"id": "edited:" + task_id, "path": directory + "/handoff.md", "content": task["excerpt"]}]
                report["tasks"].append(task)
                if task_id == request.get("task_id"):
                    report["sync_pending"] = bool(task.get("sync_pending", False))
            except FileNotFoundError:
                pass
            except (OSError, ValueError, TypeError, KeyError):
                report["warnings"].append("无法读取任务 " + name + " 的交接记忆。")
        report["tasks"].sort(key=lambda task: task.get("updated", 0), reverse=True)
        for task in report["tasks"]:
            if not owned_directory and task["id"] == request.get("task_id"):
                report["task_directory"] = task["path"].rsplit("/", 1)[0]
                break
        directory = report["task_directory"]
        if directory:
            for name in (WORKLOG_NAME, "README.md", "handoff.md"):
                try:
                    content = read(directory + "/" + name)
                    report["documents"].append({"path": directory + "/" + name, "title": name, "kind": "worklog" if name == WORKLOG_NAME else "index"})
                    if name == WORKLOG_NAME:
                        report["main_document"] = directory + "/" + name
                        report["revision"] = digest(content)
                        report["editable"] = True
                except (OSError, ValueError):
                    pass
            try:
                for name in sorted(entries(directory + "/history"))[:1997]:
                    relative = directory + "/history/" + name
                    if valid_document(relative):
                        report["documents"].append({"path": relative, "title": "历史 · " + name, "kind": "history"})
            except (OSError, ValueError):
                pass
            try:
                for name in sorted(entries(directory + "/runs"))[:2001]:
                    relative = directory + "/runs/" + name
                    if valid_document(relative):
                        report["documents"].append({"path": relative, "title": name, "kind": "run"})
            except (OSError, ValueError):
                pass
        # Go renders the portable human-readable index after this response. Its
        # writer call also uses the same no-follow confinement and edit checks.
        return report
    finally:
        os.close(root_fd)


try:
    result = main()
except Exception as error:
    result = {"error": str(error), "error_code": "conflict" if isinstance(error, DocumentConflict) else "",
              "directory": ".duo", "main_document": "", "revision": "", "editable": False,
              "tasks": [], "conflicts": [], "warnings": []}
json.dump(result, sys.stdout, ensure_ascii=False)

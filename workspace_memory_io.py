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


def digest(text):
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def managed(body):
    body = body.rstrip("\n") + "\n"
    return body + MARKER + digest(body) + " -->\n"


def unmodified(text):
    start = text.rfind(MARKER)
    return start >= 0 and text.endswith(" -->\n") and text[start + len(MARKER):-5] == digest(text[:start])


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
    return (len(parts) == 4 and parts[3] in ("README.md", "handoff.md")) or (len(parts) == 5 and parts[3] == "runs" and parts[4].endswith(".md") and bool(TASK_ID.fullmatch(parts[4][:-3])))


def document_link(source, target):
    import posixpath
    return posixpath.relpath(target, posixpath.dirname(source))


def main():
    request = json.load(sys.stdin)
    root = request["root"]
    if not os.path.isabs(root) or os.path.realpath(root) != os.path.abspath(root):
        raise ValueError("工作区记忆需要无符号链接的绝对工作区路径")
    report = {"enabled": True, "directory": ".duo", "task_directory": request.get("task_directory", ""), "exported": 0,
              "unchanged": 0, "tasks": [], "conflicts": [], "warnings": [], "documents": [], "task_directories": []}
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
                if not stat_module.S_ISREG(stat.st_mode) or stat.st_size > LIMIT:
                    raise ValueError("工作区记忆不是普通文件或超过 1 MiB")
                raw = source.read(LIMIT + 1)
                if len(raw) > LIMIT:
                    raise ValueError("工作区记忆超过 1 MiB")
                return raw.decode("utf-8")
        finally:
            os.close(fd)

    def write(relative, text, create_only=False):
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

    def entries(relative):
        fd, _ = parent(relative + "/_")
        try:
            return os.listdir(fd)
        finally:
            os.close(fd)

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
        if request["action"] == "sync":
            for file in request.get("files", []):
                write(file["path"], file["content"], file.get("create_only", False))
        if request["action"] == "document":
            relative = request.get("path", "")
            if not valid_document(relative):
                raise ValueError("只能读取工作区内的任务 Markdown 记录")
            report["path"], report["content"] = relative, read(relative)
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
                if valid_directory(request.get("task_id", ""), directory) and (report["task_directory"] == request.get("task_directory") or name != request.get("task_id")):
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
                for source in task.get("sources", []):
                    if source.get("path", "").startswith(old_base + "/"):
                        source["path"] = directory + source["path"][len(old_base):]
                if not unmodified(raw):
                    task["excerpt"] = raw[end + 5:][:2400]
                    task["sources"] = [{"id": "edited:" + task_id, "path": directory + "/handoff.md", "content": task["excerpt"]}]
                report["tasks"].append(task)
            except FileNotFoundError:
                pass
            except (OSError, ValueError, TypeError, KeyError):
                report["warnings"].append("无法读取任务 " + name + " 的交接记忆。")
        report["tasks"].sort(key=lambda task: task.get("updated", 0), reverse=True)
        for task in report["tasks"]:
            if task["id"] == request.get("task_id"):
                report["task_directory"] = task["path"].rsplit("/", 1)[0]
                break
        directory = report["task_directory"]
        if directory:
            for name in ("README.md", "handoff.md"):
                try:
                    read(directory + "/" + name)
                    report["documents"].append({"path": directory + "/" + name, "title": name, "kind": "index"})
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
    result = {"error": str(error), "directory": ".duo", "tasks": [], "conflicts": [], "warnings": []}
json.dump(result, sys.stdout, ensure_ascii=False)

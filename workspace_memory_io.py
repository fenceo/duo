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


def main():
    request = json.load(sys.stdin)
    root = request["root"]
    if not os.path.isabs(root) or os.path.realpath(root) != os.path.abspath(root):
        raise ValueError("工作区记忆需要无符号链接的绝对工作区路径")
    report = {"enabled": True, "directory": ".duo", "task_directory": "", "exported": 0,
              "unchanged": 0, "tasks": [], "conflicts": [], "warnings": []}
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

    try:
        if request["action"] == "sync":
            for file in request.get("files", []):
                write(file["path"], file["content"], file.get("create_only", False))
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
            if not TASK_ID.fullmatch(name):
                continue
            try:
                raw = read(".duo/tasks/" + name + "/handoff.md")
                end = raw.index(" -->\n")
                if not raw.startswith(TASK_MARKER):
                    continue
                task = json.loads(raw[len(TASK_MARKER):end])
                if task.get("id") != name or task.get("path") != ".duo/tasks/" + name + "/README.md" or len(task.get("sources", [])) > 6:
                    continue
                if not unmodified(raw):
                    task["excerpt"] = raw[end + 5:][:2400]
                    task["sources"] = [{"id": "edited:" + name, "path": ".duo/tasks/" + name + "/handoff.md", "content": task["excerpt"]}]
                report["tasks"].append(task)
            except FileNotFoundError:
                pass
            except (OSError, ValueError, TypeError, KeyError):
                report["warnings"].append("无法读取任务 " + name + " 的交接记忆。")
        report["tasks"].sort(key=lambda task: task.get("updated", 0), reverse=True)
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

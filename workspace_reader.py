"""Read-only workspace helper sent over the task's existing WSL/SSH transport."""
import base64
import json
import os
import pathlib
import stat
import subprocess
import sys
import tempfile

PREVIEW = 128 * 1024
DOWNLOAD = 32 * 1024 * 1024

def run_git(args, limit=512 * 1024, allow_missing=False):
    # File-backed output avoids buffering a very large repository diff in memory.
    with tempfile.TemporaryFile() as out, tempfile.TemporaryFile() as err:
        proc = subprocess.run(args, stdout=out, stderr=err, timeout=12, env=dict(os.environ,GIT_OPTIONAL_LOCKS="0"))
        out.seek(0)
        raw = out.read(limit + 1)
        if proc.returncode and not (allow_missing and proc.returncode == 1):
            err.seek(0)
            raise ValueError(err.read(2048).decode("utf-8", "replace") or "Git 读取失败")
        return raw[:limit], len(raw) > limit

def main(request):
    root = os.path.realpath(request["root"])
    relative = request["path"]
    action = request["action"]
    parts = relative.split("/") if relative else []
    if relative.startswith("/") or any(p in ("..", ".git") for p in parts) or any(c in relative for c in "\\:\x00\r\n"):
        raise ValueError("路径不在工作目录内")
    result = dict(path=relative, items=[], content="", size=0, binary=False, truncated=False, message="")
    target = os.path.join(root, *parts)
    cursor = root
    for part in parts:
        cursor = os.path.join(cursor, part)
        if os.path.islink(cursor):
            raise ValueError("符号链接不可在文件面板中打开")
    if action in ("changes", "diff"):
        args = ["git", "--no-pager", "--literal-pathspecs", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=", "-c", "core.untrackedCache=false", "-C", root]
        try:
            run_git(args + ["rev-parse", "--git-dir"], 4096)
        except (OSError, ValueError):
            result["message"] = "当前目录不是 Git 仓库，或该环境未安装 Git；仍可浏览和下载文件"
            return result
        keys, too_many = run_git(args + ["config", "--name-only", "--get-regexp", r"^filter\..*\.(clean|process|required)$"], 65536, True)
        if too_many:
            raise ValueError("Git 过滤器配置超过读取限制")
        for key in keys.decode().splitlines():
            if key.startswith("filter."):
                driver = key.rsplit(".", 1)[0]
                args += ["-c", driver + ".clean=", "-c", driver + ".process=", "-c", driver + ".required=false"]
        if action == "changes":
            prefix = run_git(args + ["rev-parse", "--show-prefix"], 8192)[0].decode().strip()
            raw, truncated = run_git(args + ["status", "--porcelain=v1", "-z", "--untracked-files=normal", "--ignore-submodules=all", "--", "."], 1024 * 1024)
            values = iter(raw.decode("utf-8", "replace").split("\0"))
            for value in values:
                if len(value) < 4:
                    continue
                status, name = value[:2], value[3:]
                if "R" in status or "C" in status:
                    next(values, None)
                if not name.startswith(prefix):
                    continue
                name = name[len(prefix):]
                if name.startswith("/") or ".." in name.split("/") or ".git" in name.split("/"):
                    continue
                result["items"].append(dict(name=pathlib.PurePosixPath(name).name, path=name.rstrip("/"), directory=name.endswith("/"), status=status, size=0))
                if len(result["items"]) >= 500:
                    truncated = True
                    break
            result.update(truncated=truncated, message="工作目录当前 Git 状态，包含原有改动；并非本轮修改清单")
            return result
        for label, options in [("未暂存改动", []), ("已暂存改动", ["--cached"])]:
            raw, truncated = run_git(args + ["diff", "--no-ext-diff", "--no-textconv", "--ignore-submodules=all", "--no-color"] + options + ["--", relative])
            result["truncated"] |= truncated
            if raw:
                result["content"] += "── " + label + " ──\n" + raw.decode("utf-8", "replace") + "\n"
        if not result["content"]:
            result["message"] = "没有已跟踪文件的差异。新文件可在“内容”中查看。"
        return result
    # Every path segment is opened relative to an owned directory descriptor,
    # with O_NOFOLLOW, to reject symlink swaps as well as ../ traversal.
    fd = os.open(root, os.O_RDONLY | os.O_DIRECTORY)
    try:
        for i, part in enumerate(parts):
            flags = os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK
            if i < len(parts) - 1 or action == "list":
                flags |= os.O_DIRECTORY
            next_fd = os.open(part, flags, dir_fd=fd)
            os.close(fd)
            fd = next_fd
        info = os.fstat(fd)
        result["size"] = info.st_size
        if action == "list":
            if not stat.S_ISDIR(info.st_mode):
                raise ValueError("该路径不是目录")
            with os.scandir(fd) as entries:
                for entry in entries:
                    if entry.name.lower() == ".git":
                        continue
                    if len(result["items"]) >= 500:
                        result["truncated"] = True
                        break
                    try:
                        size = entry.stat(follow_symlinks=False).st_size
                    except OSError:
                        size = 0
                    directory = entry.is_dir(follow_symlinks=False)
                    result["items"].append(dict(name=entry.name, path="/".join(parts + [entry.name]), directory=directory, size=size, blocked=entry.is_symlink() or not (directory or entry.is_file(follow_symlinks=False))))
            result["items"].sort(key=lambda v: (not v["directory"], v["name"].lower()))
            return result
        if not stat.S_ISREG(info.st_mode):
            raise ValueError("只支持普通文件")
        limit = DOWNLOAD if action == "download" else PREVIEW
        if action == "download" and info.st_size > limit:
            raise ValueError("下载文件不能超过 32 MiB")
        with os.fdopen(os.dup(fd), "rb") as stream:
            raw = stream.read(limit + 1)
        if len(raw) > limit:
            if action == "download":
                raise ValueError("下载文件不能超过 32 MiB")
            result["truncated"] = True
            raw = raw[:limit]
        if action == "download":
            result["data"] = base64.b64encode(raw).decode("ascii")
        else:
            try:
                import codecs
                result["content"] = codecs.getincrementaldecoder("utf-8")().decode(raw, final=not result["truncated"])
                result["binary"] = b"\0" in raw
            except UnicodeDecodeError:
                result["binary"] = True
            if result["binary"]:
                result.update(content="", message="二进制或非 UTF-8 文件，请下载后查看")
        return result
    finally:
        os.close(fd)

try:
    print(json.dumps(main(json.load(sys.stdin)), ensure_ascii=True))
except Exception as error:
    print(json.dumps({"error": str(error)}, ensure_ascii=True))

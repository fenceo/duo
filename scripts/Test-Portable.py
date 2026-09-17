"""Exercise the packaged executables using isolated data; never starts Codex or Feishu."""
import http.cookiejar
import urllib.error
import hashlib
from contextlib import contextmanager
import json
import pathlib
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.request
import zipfile

ROOT = pathlib.Path(__file__).resolve().parents[1]
PACKAGE = ROOT / "dist/Jianzuo-portable-windows-x64"
SERVICE = PACKAGE / "jianzuo-service.exe"
LAUNCHER = PACKAGE / "简作.exe"
FLAGS = subprocess.CREATE_NO_WINDOW
PASSWORD = "便携测试-password"


@contextmanager
def database(path):
    connection = sqlite3.connect(path)
    try:
        with connection:
            yield connection
    finally:
        connection.close()


def run(args, data=None):
    return subprocess.run([str(x) for x in args], input=data, capture_output=True, timeout=40,
                          creationflags=FLAGS)


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def start(directory, port):
    proc = subprocess.Popen([str(SERVICE), "--data", str(directory), "--managed"],
                            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                            creationflags=FLAGS)
    url = f"http://127.0.0.1:{port}"
    client = urllib.request.build_opener(urllib.request.ProxyHandler({}),
                                         urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
    try:
        for _ in range(80):
            if proc.poll() is not None:
                raise AssertionError(proc.stderr.read().decode("utf-8", "replace"))
            try:
                with client.open(url + "/healthz", timeout=0.5) as res:
                    assert json.load(res)["version"] == "0.12.0-portable"
                    return proc, client, url
            except OSError:
                time.sleep(.1)
        raise AssertionError("service startup timed out")
    except BaseException:
        stop(proc)
        raise


def stop(proc):
    if proc.poll() is None:
        proc.stdin.write(b"stop\n")
        proc.stdin.flush()
    proc.communicate(timeout=30)
    assert proc.returncode == 0


def api(client, url, path, method="GET", payload=None, csrf=""):
    req = urllib.request.Request(url + "/api/" + path, method=method,
                                 data=None if payload is None else json.dumps(payload).encode(),
                                 headers={"Content-Type": "application/json", "Origin": url,
                                          "X-CSRF-Token": csrf})
    with client.open(req, timeout=10) as res:
        return json.load(res)


test_root = ROOT / "_testdata"
test_root.mkdir(exist_ok=True)
with tempfile.TemporaryDirectory(prefix="portable-中文 path-", dir=test_root) as tmp:
    directory = pathlib.Path(tmp)
    port = free_port()
    config = {"listen": f"127.0.0.1:{port}", "default_environment": "windows",
              "environments": [{"id": "windows", "name": "中文执行环境", "type": "windows",
                                "codex": "codex.exe", "workspaces": [str(directory)]}],
              "feishu": {"enabled": False}, "access": {"lan": "", "tailscale": ""}}
    data = json.dumps({"password": PASSWORD, "config": config}, ensure_ascii=False).encode()
    init = run([SERVICE, "--data", directory, "--portable-init"], data)
    assert init.returncode == 0, init.stderr.decode("utf-8", "replace")
    saved_config = (directory / "config.json").read_bytes()
    assert PASSWORD.encode() not in saved_config
    assert run([SERVICE, "--data", directory, "--portable-init"], data).returncode != 0
    assert (directory / "config.json").read_bytes() == saved_config

    proc, client, url = start(directory, port)
    try:
        try:
            api(client, url, "updates")
            raise AssertionError("Update settings require login")
        except urllib.error.HTTPError as error:
            assert error.code == 401
        csrf = api(client, url, "login", "POST", {"password": PASSWORD})["csrf"]
        initial_update = api(client, url, "updates")
        assert initial_update["current"] == "0.12.0-portable"
        assert not initial_update.get("checked")
        try:
            api(client, url, "updates", "PUT", {"repository": "owner/jianzuo"})
            raise AssertionError("Update source writes require CSRF")
        except urllib.error.HTTPError as error:
            assert error.code == 403
        update = api(client, url, "updates", "PUT", {"repository": "https://github.com/owner/jianzuo"}, csrf)
        assert update["repository"] == "owner/jianzuo" and update["state"] == "unchecked"
        try:
            api(client, url, "updates", "PUT", {"repository": "http://127.0.0.1/private"}, csrf)
            raise AssertionError("Non-GitHub update sources must be rejected")
        except urllib.error.HTTPError as error:
            assert error.code == 400
        task = api(client, url, "tasks", "POST", {"title": "便携数据保留", "workspace": str(directory),
                   "environment_id": "windows"}, csrf)["task"]
        path = "tasks/" + task["id"]
        api(client, url, path + "/note", "PUT", {"content": "# 知识\n重启后应保留", "revision": 0}, csrf)
        api(client, url, path + "/scratch", "POST", {"content": "待调试便签"}, csrf)
        cfg = api(client, url, "settings")["config"]
        cfg["access"] = {"lan": url, "tailscale": "http://100.100.10.20:8789"}
        api(client, url, "settings", "PUT", cfg, csrf)
        # A rejected duplicate must not openStore() and reset existing task status.
        with database(directory / "jianzuo.db") as db:
            db.execute("UPDATE tasks SET status='running' WHERE id=?", (task["id"],))
        assert run([SERVICE, "--data", directory, "--managed"], b"stop\n").returncode != 0
        assert api(client, url, path)["task"]["status"] == "running"
        with database(directory / "jianzuo.db") as db:
            db.execute("UPDATE tasks SET status='idle' WHERE id=?", (task["id"],))
        other = directory / "conflicting-port"
        assert run([SERVICE, "--data", other, "--portable-init"], data).returncode == 0
        with database(other / "jianzuo.db") as db:
            db.execute("INSERT INTO tasks VALUES ('sentinel','sentinel',?,'','','running',1,1)", (str(directory),))
        assert run([SERVICE, "--data", other, "--managed"], b"stop\n").returncode != 0
        with database(other / "jianzuo.db") as db:
            assert db.execute("SELECT status FROM tasks WHERE id='sentinel'").fetchone()[0] == "running"
    finally:
        stop(proc)
    proc, client, url = start(directory, port)
    try:
        api(client, url, "login", "POST", {"password": PASSWORD})
        assert api(client, url, path)["task"]["title"] == "便携数据保留"
        assert api(client, url, path + "/note")["content"] == "# 知识\n重启后应保留"
        assert len(api(client, url, path + "/scratch")) == 1
        assert api(client, url, "settings")["config"]["access"]["lan"] == url
        assert api(client, url, "updates")["repository"] == "owner/jianzuo"
        # EOF is the launcher-crash path: backend must release its port and database.
        proc.stdin.close()
        proc.stdin = None
        proc.communicate(timeout=30)
        assert proc.returncode == 0
    finally:
        if proc.poll() is None:
            stop(proc)
    smoke = run([LAUNCHER, "--smoke", "--data", directory])
    assert smoke.returncode == 0, smoke.stderr.decode("utf-8", "replace")
    with socket.socket() as sock:
        assert sock.connect_ex(("127.0.0.1", port)) != 0

with zipfile.ZipFile(ROOT / "dist/Jianzuo-portable-windows-x64.zip") as archive:
    assert set(archive.namelist()) == {"简作.exe", "jianzuo-service.exe", "使用说明.md", "THIRD-PARTY-NOTICES.txt"}
package_zip = ROOT / "dist/Jianzuo-portable-windows-x64.zip"
assert hashlib.sha256(package_zip.read_bytes()).hexdigest() == pathlib.Path(str(package_zip)+".sha256").read_text().split()[0]
print("PASS: initialization, Unicode password/path, non-overwrite, login, notes, settings, duplicate lock,")
print("      port collision preserves state, restart persistence, EOF shutdown, launcher smoke, clean ZIP.")
print("      update version/source, auth/CSRF, validation, source persistence, ZIP checksum.")

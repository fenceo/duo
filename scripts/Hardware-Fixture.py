"""Loopback-only simulated Linux/U-Boot console; never opens a physical port."""
import json
import os
import pathlib
import socket
import tempfile
import threading
import time

root = pathlib.Path(__file__).resolve().parents[1] / "_testdata"
root.mkdir(exist_ok=True)
folder = pathlib.Path(tempfile.mkdtemp(prefix="serial-wire-", dir=root))
server = socket.socket()
server.bind(("127.0.0.1", 0))
server.listen(4)
print(json.dumps({"pid":os.getpid(),"port":server.getsockname()[1],"folder":str(folder)}),flush=True)
lock = threading.Lock()

def client(conn):
    command = bytearray()
    escape = bytearray()
    prompt = b"root@board:~# "
    history = b"help"
    try:
        # Fragment a UTF-8 character across network reads, then ask for a cursor
        # report. Opening/replaying this display must not transmit any response.
        banner = "\x1b[32mLinux 串口模拟设备\x1b[0m\r\n".encode()
        for part in [banner[:14],banner[14:15],banner[15:]]:
            conn.sendall(part)
            time.sleep(.025)
        conn.sendall(b"\x1b[6n"+prompt)
        while True:
            data = conn.recv(4096)
            if not data:
                return
            with lock, (folder / "wire.bin").open("ab") as f:
                f.write(data)
            for b in data:
                if escape:
                    escape.append(b)
                    if bytes(escape) == b"\x1b[A":
                        command = bytearray(history)
                        conn.sendall(b"\r\x1b[2K"+prompt+history)
                        escape.clear()
                    elif bytes(escape) == b"\x1b[B":
                        command.clear();conn.sendall(b"\r\x1b[2K"+prompt);escape.clear()
                    elif len(escape)>3:
                        escape.clear()
                    continue
                if b == 27:
                    escape.append(b)
                elif b == 9:
                    if command == b"he":
                        command.extend(b"lp");conn.sendall(b"lp")
                elif b == 3:
                    command.clear();conn.sendall(b"^C\r\n"+prompt)
                elif b in (8,127):
                    if command:
                        command.pop();conn.sendall(b"\b \b")
                elif b in (10,13):
                    text = bytes(command).decode("utf-8", "replace")
                    if command:
                        history=bytes(command)
                    command.clear()
                    reply = "help  printenv  version" if text=="help" else "U-Boot 2026.01 (fixture)" if text=="version" else text[5:] if text.startswith("echo ") else ""
                    conn.sendall(b"\r\n"+reply.encode()+b"\r\n"+prompt)
                else:
                    command.append(b);conn.sendall(bytes([b]))
    except (ConnectionError,OSError):
        pass
    finally:
        conn.close()

while True:
    conn,_=server.accept()
    threading.Thread(target=client,args=(conn,),daemon=True).start()

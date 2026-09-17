"""Loopback-only relay simulator. Never opens a physical serial port."""
import json
import os
import pathlib
import socket
import tempfile
import threading

root = pathlib.Path(__file__).resolve().parents[1] / "_testdata"
root.mkdir(exist_ok=True)
folder = pathlib.Path(tempfile.mkdtemp(prefix="relay-wire-",dir=root))
server = socket.socket()
server.bind(("127.0.0.1",0))
server.listen(4)
print(json.dumps({"pid":os.getpid(),"port":server.getsockname()[1],"folder":str(folder)}),flush=True)
lock = threading.Lock()
coils = {}
def client(conn):
    pending = b""
    try:
        while True:
            data = conn.recv(1024)
            if not data:
                return
            pending += data
            while len(pending) >= 4:
                frame,pending=pending[:4],pending[4:]
                assert frame[0]==0xa0 and sum(frame[:3]) % 256==frame[3]
                channel,op=frame[1:3]
                with lock:
                    with (folder / "wire.bin").open("ab") as f:
                        f.write(frame)
                    if op in (0,2):coils[channel]=0
                    if op in (1,3):coils[channel]=1
                    state=coils.get(channel,0)
                if op in (2,3,5):
                    reply=bytes([0xa0,channel,state,(0xa0+channel+state)%256])
                    conn.sendall(reply[:1]);conn.sendall(reply[1:])
    except (ConnectionError,OSError):
        pass
    finally:
        conn.close()
while True:
    conn,_=server.accept()
    threading.Thread(target=client,args=(conn,),daemon=True).start()

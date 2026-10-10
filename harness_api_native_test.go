package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Opt-in native validation in the selected WSL user, isolated from ~/.dsh.
// Both Harness and its fake provider run inside WSL; no paid endpoint is used.
func TestHarnessAPINativeWSL(t *testing.T) {
	if os.Getenv("DUO_HARNESS_API_WSL_FIXTURE") != "1" {
		t.Skip("opt-in installed WSL Harness with loopback fixture")
	}
	env := Environment{ID: "synthetic-harness-api", Type: "wsl", Distro: os.Getenv("DUO_HARNESS_API_WSL_DISTRO"), User: os.Getenv("DUO_HARNESS_API_WSL_USER"), Harness: os.Getenv("DUO_HARNESS_API_WSL_BINARY"), HarnessProvider: harnessAPIProvider, HarnessModel: "fixture-model"}
	if env.Distro == "" || env.User == "" || env.Harness == "" {
		t.Fatal("fixture requires exact distro, user and installed CLI path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	mktemp := commandWithContext(ctx, environmentProbeCommand(env, "mktemp", "-d", "/tmp/duo-harness-api-XXXXXX"))
	out, err := mktemp.Output()
	if err != nil {
		t.Fatal("cannot create isolated WSL fixture")
	}
	home := strings.TrimSpace(string(out))
	if !strings.HasPrefix(home, "/tmp/duo-harness-api-") || strings.ContainsAny(home, "\r\n\x00") {
		t.Fatal("invalid isolated fixture path")
	}
	t.Cleanup(func() {
		closeHarnessRuntimes()
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		cmd := commandWithContext(cleanupCtx, environmentProbeCommand(env, "python3", "-c", "import os,sys,shutil; p=sys.argv[1]; assert os.path.dirname(p)=='/tmp' and os.path.basename(p).startswith('duo-harness-api-') and not os.path.islink(p); shutil.rmtree(p)", home))
		if cmd.Run() != nil {
			t.Error("isolated fixture cleanup failed")
		}
	})
	const serverScript = `import sys,json,threading,http.server
hits=[]
class Handler(http.server.BaseHTTPRequestHandler):
 def log_message(self,*args): pass
 def do_POST(self):
  request=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
  valid=self.path=='/v1/chat/completions' and self.headers.get('Authorization')=='Bearer synthetic-harness-secret'
  hits.append({'model':request.get('model'),'authenticated':valid})
  if not valid:
   self.send_error(403); return
  self.send_response(200); self.send_header('Content-Type','text/event-stream'); self.end_headers()
  first={'id':'fixture','object':'chat.completion.chunk','created':1,'model':request['model'],'choices':[{'index':0,'delta':{'role':'assistant','content':'DUO_HARNESS_API_OK'},'finish_reason':None}]}
  last={'id':'fixture','object':'chat.completion.chunk','created':1,'choices':[{'index':0,'delta':{},'finish_reason':'stop'}],'usage':{'prompt_tokens':20,'completion_tokens':5,'total_tokens':25}}
  self.wfile.write(('data: '+json.dumps(first)+'\n\ndata: '+json.dumps(last)+'\n\ndata: [DONE]\n\n').encode()); self.wfile.flush()
server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
threading.Thread(target=server.serve_forever,daemon=True).start()
print(server.server_port,flush=True)
sys.stdin.readline()
server.shutdown()
print(json.dumps(hits),flush=True)
`
	server := commandWithContext(ctx, environmentProbeCommand(env, "python3", "-u", "-c", serverScript))
	in, err := server.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	server.Stderr = io.Discard
	if err := server.Start(); err != nil {
		t.Fatal("cannot start WSL loopback fixture")
	}
	defer func() {
		in.Close()
		if server.ProcessState == nil {
			server.Process.Kill()
			server.Wait()
		}
	}()
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal("no fixture server port")
	}
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || port < 1 || port > 65535 {
		t.Fatal("bad fixture port")
	}
	req := syntheticHarnessAPI()
	req.BaseURL = "http://127.0.0.1:" + strconv.Itoa(port) + "/v1"
	old, err := readNativeAccountFiles(ctx, env, home, "deepseek-harness")
	if err != nil {
		t.Fatal(err)
	}
	next, err := mergeHarnessAPI(old, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeNativeAccountFiles(ctx, env, home, "deepseek-harness", old, next); err != nil {
		t.Fatal(err)
	}
	actual, err := readNativeAccountFiles(ctx, env, home, "deepseek-harness")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual[".credentials.yaml"], next[".credentials.yaml"]) {
		t.Fatal("native credential save mismatch")
	}
	c := runtimeConfig(Config{}, env)
	c.EngineEnv = map[string]string{"DSH_HOME": home}
	task := Task{ID: "native-harness-api", Engine: "deepseek-harness", Workspace: home, Model: req.Model, Mode: &WorkMode{Permission: "read", Approval: "never", AllowNetwork: boolPtr(true)}}
	closeHarnessRuntimes()
	session, result, err := runHarnessACP(ctx, c, task, "Reply with the local fixture marker.", func(string, string) {})
	if err != nil {
		t.Fatal(err)
	}
	if session == "" || !strings.Contains(result, "DUO_HARNESS_API_OK") {
		t.Fatal("native ACP response missing")
	}
	closeHarnessRuntimes()
	io.WriteString(in, "done\n")
	line, err = reader.ReadString('\n')
	if err != nil {
		t.Fatal("fixture summary missing")
	}
	var hits []struct {
		Model         string `json:"model"`
		Authenticated bool   `json:"authenticated"`
	}
	if json.Unmarshal([]byte(line), &hits) != nil || len(hits) != 1 || hits[0].Model != req.Model || !hits[0].Authenticated {
		t.Fatal("native credential or route was not used")
	}
	if err := server.Wait(); err != nil {
		t.Fatal("fixture server did not stop")
	}
	t.Log("selected WSL user saved native patch and credential files; installed dsh returned through Duo ACP; one authenticated loopback model request, zero paid calls")
}

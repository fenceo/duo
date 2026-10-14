package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMimoTargetCommandUsesDirectWSLExecution(t *testing.T) {
	value := `{"model":"duo-mimo/mimo-v2.6-pro","permission":{"*":"ask"}}`
	c := Config{Distro: "synthetic-distro", User: "synthetic-user"}
	cmd := mimoTargetCommand(c, withEngineEnv([]string{"python3", "-c", claudeLauncher, "/workspace with 'quotes'", "/synthetic/mimo", "acp"}, map[string]string{"MIMOCODE_CONFIG_CONTENT": value})...)
	if len(cmd.Args) < 7 || strings.Join(cmd.Args[1:6], "|") != "-d|synthetic-distro|-u|synthetic-user|--exec" || cmd.Args[7] != "MIMOCODE_CONFIG_CONTENT="+value {
		t.Fatal("MiMo WSL launch must preserve JSON and bypass shell interpretation")
	}
	for _, entry := range cmd.Env {
		if !strings.HasPrefix(entry, "SystemRoot=") && !strings.HasPrefix(entry, "WINDIR=") {
			t.Fatal("Windows environment leaked into WSL")
		}
	}
}

// No CLI or inference: exercise the actual Windows-to-WSL argument transport
// with synthetic JSON, including shell metacharacters and literal newlines.
func TestMimoWSLJSONEnvironmentNative(t *testing.T) {
	if os.Getenv("DUO_MIMO_WSL_TRANSPORT") != "1" {
		t.Skip("explicit WSL transport regression only")
	}
	c := Config{Distro: os.Getenv("DUO_MIMO_WSL_DISTRO"), User: os.Getenv("DUO_MIMO_WSL_USER")}
	if c.Distro == "" {
		t.Fatal("provide an exact WSL distribution")
	}
	value := `{"model":"duo-mimo/mimo-v2.6-pro","provider":{"duo-mimo":{"models":{"mimo-v2.6-pro":{"interleaved":{"field":"reasoning_content"}}}}},"literal":"$HOME $(printf expanded) ' quoted \\ path"}` + "\n"
	vars := map[string]string{"MIMOCODE_CONFIG_CONTENT": value, "MIMOCODE_PERMISSION": `{"*":"allow"}`}
	args := withEngineEnv([]string{"python3", "-c", "import os,json; print(json.dumps({k:os.environ[k] for k in ['MIMOCODE_CONFIG_CONTENT','MIMOCODE_PERMISSION']}))"}, vars)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := commandWithContext(ctx, mimoTargetCommand(c, args...)).Output()
	if err != nil {
		t.Fatal("transport failed", err)
	}
	var got map[string]string
	if json.Unmarshal(output, &got) != nil || got["MIMOCODE_CONFIG_CONTENT"] != value || got["MIMOCODE_PERMISSION"] != vars["MIMOCODE_PERMISSION"] {
		t.Fatal("WSL changed the JSON environment before the engine started")
	}
	if !json.Valid([]byte(got["MIMOCODE_CONFIG_CONTENT"])) {
		t.Fatal("synthetic JSON was interpreted")
	}
}

// Run the installed official WSL CLI against a server inside that same WSL
// environment. Fresh home, fake key and native data; never use real inference.
func TestMimoACPNativeWSLLocalProvider(t *testing.T) {
	if os.Getenv("DUO_MIMO_WSL_LOCAL_PROVIDER") != "1" {
		t.Skip("explicit isolated WSL native regression only")
	}
	c := Config{Distro: os.Getenv("DUO_MIMO_WSL_DISTRO"), User: os.Getenv("DUO_MIMO_WSL_USER"), Mimo: os.Getenv("DUO_MIMO_WSL_BINARY")}
	if c.Distro == "" || c.Mimo == "" {
		t.Fatal("provide exact distribution and official binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	server := commandWithContext(ctx, mimoTargetCommand(c, "python3", "-u", "-c", mimoWSLProviderFixture))
	stdin, err := server.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &cappedOutput{limit: 4096}
	server.Stderr = stderr
	if err = server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		if err := server.Wait(); err != nil {
			t.Error("WSL synthetic server cleanup failed", err)
		}
	}()
	reader := bufio.NewReader(stdout)
	line, err := reader.ReadBytes('\n')
	var paths struct{ Home, Workspace, Config string }
	if err != nil || json.Unmarshal(line, &paths) != nil || paths.Home == "" {
		t.Fatal("WSL synthetic server did not start")
	}
	c.EngineEnv = map[string]string{"MIMOCODE_HOME": paths.Home, "HOME": paths.Home, "MIMOCODE_CONFIG": paths.Config, "MIMOCODE_DISABLE_DEFAULT_PLUGINS": "1", "MIMOCODE_DISABLE_MODELS_FETCH": "1", "MIMO_API_KEY": "", "OPENAI_API_KEY": "", "OPENAI_BASE_URL": "", "ANTHROPIC_API_KEY": "", "ANTHROPIC_AUTH_TOKEN": "", "CLAUDE_CODE_OAUTH_TOKEN": ""}
	task := Task{ID: "wsl-native-loopback", Engine: "mimo", Workspace: paths.Workspace, Mode: &WorkMode{Permission: "full"}}
	for _, model := range []string{"mimo-v2.6-pro", "mimo-v2.6-flash"} {
		task.Model = "duo-mimo/" + model
		session, result, err := runMimoACP(ctx, c, task, "synthetic WSL turn "+model, func(string, string) {})
		if err != nil || result != "ok" || session == "" || (task.Session != "" && task.Session != session) {
			t.Fatalf("WSL native model/history/export failed: %s %q %v", model, result, err)
		}
		task.Session = session
	}
	stdin.Close()
	line, err = reader.ReadBytes('\n')
	var evidence struct {
		Models  []string
		History bool
	}
	valid := err == nil && json.Unmarshal(line, &evidence) == nil && len(evidence.Models) >= 2 && evidence.History
	if valid {
		valid = evidence.Models[0] == "mimo-v2.6-pro" && evidence.Models[len(evidence.Models)-1] == "mimo-v2.6-flash"
		for _, model := range evidence.Models {
			valid = valid && (model == "mimo-v2.6-pro" || model == "mimo-v2.6-flash")
		}
	}
	// The official agent can use the selected model for a session title too.
	// Verify routes and history, rather than assuming one request per turn.
	if !valid {
		t.Fatalf("WSL native request evidence mismatch: models=%v history=%v read=%v", evidence.Models, evidence.History, err)
	}
	t.Log("official WSL ACP startup, two selected models, short reply, durable resume and export passed; fake key and loopback only")
}

const mimoWSLProviderFixture = `import os,sys,json,tempfile,pathlib,threading,shutil
from http.server import BaseHTTPRequestHandler,HTTPServer
root=pathlib.Path(tempfile.mkdtemp(prefix='duo-mimo-wsl-')).resolve()
models=[]; history=False
class Handler(BaseHTTPRequestHandler):
 def log_message(self,*args): pass
 def do_GET(self):
  self.send_response(200);self.send_header('Content-Type','application/json');self.end_headers()
  self.wfile.write(json.dumps({'data':[{'id':'mimo-v2.5'}]}).encode())
 def do_POST(self):
  global history
  body=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
  models.append(body['model'])
  if body['model']=='mimo-v2.6-flash': history='synthetic WSL turn mimo-v2.6-pro' in json.dumps(body.get('messages',[]))
  self.send_response(200);self.send_header('Content-Type','text/event-stream');self.end_headers()
  for delta,finish in [({'role':'assistant'},None),({'content':'o'},None),({'content':'k'},None),({},'stop')]:
   chunk={'id':'synthetic-wsl','object':'chat.completion.chunk','created':1,'model':body['model'],'choices':[{'index':0,'delta':delta,'finish_reason':finish}]}
   self.wfile.write(('data: '+json.dumps(chunk)+'\n\n').encode());self.wfile.flush()
  self.wfile.write(b'data: [DONE]\n\n');self.wfile.flush()
server=HTTPServer(('127.0.0.1',0),Handler)
threading.Thread(target=server.serve_forever,daemon=True).start()
workspace=root/"workspace 'quoted' $(literal)";workspace.mkdir()
config=root/'profile with spaces.json'
config.write_text(json.dumps({'model':'duo-mimo/mimo-v2.5','enabled_providers':['duo-mimo'],'provider':{'duo-mimo':{'npm':'@ai-sdk/openai-compatible','only_configured_models':True,'models':{'mimo-v2.5':{'name':'mimo-v2.5'}},'options':{'baseURL':'http://127.0.0.1:'+str(server.server_port)+'/v1','apiKey':'synthetic-key'}}}}))
try:
 print(json.dumps({'Home':str(root),'Workspace':str(workspace),'Config':str(config)}),flush=True)
 sys.stdin.read()
 print(json.dumps({'Models':models,'History':history}),flush=True)
finally:
 server.shutdown();server.server_close()
 if root.parent != pathlib.Path(tempfile.gettempdir()).resolve() or not root.name.startswith('duo-mimo-wsl-'): raise RuntimeError('invalid cleanup root')
 shutil.rmtree(root)
`

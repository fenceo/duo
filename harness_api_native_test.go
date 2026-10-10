package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
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
 def do_GET(self):
  if self.path=='/mcp/hardware':
   self.send_error(405); return
  valid=self.path=='/v1/models' and self.headers.get('Authorization')=='Bearer synthetic-harness-secret'
  hits.append({'model':'model-list','authenticated':valid})
  if not valid:
   self.send_error(403); return
  self.send_response(200); self.send_header('Content-Type','application/json'); self.end_headers()
  self.wfile.write(json.dumps({'data':[{'id':'fixture-model'},{'id':'fixture-alternative'}]}).encode())
 def do_POST(self):
  request=json.loads(self.rfile.read(int(self.headers['Content-Length'])))
  if self.path=='/mcp/hardware':
   auth=self.headers.get('Authorization')
   if auth not in ['Bearer synthetic-mcp-first','Bearer synthetic-mcp-second']:
    self.send_error(403); return
   method=request.get('method')
   if 'id' not in request:
    self.send_response(202); self.end_headers(); return
   if method=='initialize':
    result={'protocolVersion':request['params']['protocolVersion'],'capabilities':{'tools':{}},'serverInfo':{'name':'jianzuo-hardware','version':'fixture'}}
   elif method=='tools/list':
    result={'tools':[{'name':'duo_probe','description':'Return a harmless synthetic authorization marker','inputSchema':{'type':'object','properties':{},'additionalProperties':False},'annotations':{'readOnlyHint':True,'destructiveHint':False}}]}
   elif method=='tools/call' and request['params']['name']=='duo_probe':
    hits.append({'model':'mcp-call','authenticated':True})
    result={'content':[{'type':'text','text':'DUO_MCP_AUTH:'+auth}],'isError':False}
   elif method=='ping': result={}
   else:
    self.send_error(400); return
   self.send_response(200); self.send_header('Content-Type','application/json'); self.end_headers()
   self.wfile.write(json.dumps({'jsonrpc':'2.0','id':request['id'],'result':result}).encode()); return
  valid=self.path=='/v1/chat/completions' and self.headers.get('Authorization')=='Bearer synthetic-harness-secret'
  hits.append({'model':request.get('model'),'authenticated':valid})
  if not valid:
   self.send_error(403); return
  self.send_response(200); self.send_header('Content-Type','text/event-stream'); self.end_headers()
  mcp_users=[i for i,m in enumerate(request['messages']) if m.get('role')=='user' and 'DUO_MCP_PROBE' in str(m.get('content'))]
  if mcp_users:
   last_user=mcp_users[-1]
   available=[x['function']['name'] for x in request.get('tools',[]) if 'duo_probe' in x.get('function',{}).get('name','')]
   tools=[m for m in request['messages'][last_user+1:] if m.get('role')=='tool']
   if available and not tools:
    delta={'role':'assistant','tool_calls':[{'index':0,'id':'mcp-probe-'+str(last_user),'type':'function','function':{'name':available[0],'arguments':'{}'}}]}; finish='tool_calls'
   else:
    delta={'role':'assistant','content':'DUO_MCP_RESULT:'+str(tools[-1].get('content')) if tools else 'DUO_MCP_UNMOUNTED'}; finish='stop'
   first={'id':'mcp-fixture','object':'chat.completion.chunk','created':1,'model':request['model'],'choices':[{'index':0,'delta':delta,'finish_reason':None}]}
   last={'id':'mcp-fixture','object':'chat.completion.chunk','created':1,'choices':[{'index':0,'delta':{},'finish_reason':finish}]}
   self.wfile.write(('data: '+json.dumps(first)+'\n\ndata: '+json.dumps(last)+'\n\ndata: [DONE]\n\n').encode()); self.wfile.flush(); return
  if any(m.get('role')=='user' and 'DUO_PERMISSION_PROBE' in str(m.get('content')) for m in request.get('messages',[])):
   tools=[m for m in request['messages'] if m.get('role')=='tool']
   if not tools:
    args=json.dumps({'command':'printf DUO_PERMISSION_TOOL_EXECUTED','description':'Print one harmless synthetic permission marker','sandbox_permissions':'danger-full-access','justification':'Isolated loopback approval fixture; no user files are touched.'})
    delta={'role':'assistant','tool_calls':[{'index':0,'id':'permission-probe','type':'function','function':{'name':'bash','arguments':args}}]}
    finish='tool_calls'
   else:
    delta={'role':'assistant','content':'DUO_PERMISSION_RESULT:'+str(tools[-1].get('content'))}
    finish='stop'
   first={'id':'permission-fixture','object':'chat.completion.chunk','created':1,'model':request['model'],'choices':[{'index':0,'delta':delta,'finish_reason':None}]}
   last={'id':'permission-fixture','object':'chat.completion.chunk','created':1,'choices':[{'index':0,'delta':{},'finish_reason':finish}],'usage':{'prompt_tokens':20,'completion_tokens':5,'total_tokens':25}}
   self.wfile.write(('data: '+json.dumps(first)+'\n\ndata: '+json.dumps(last)+'\n\ndata: [DONE]\n\n').encode()); self.wfile.flush(); return
  user_content=[m.get('content') for m in request.get('messages',[]) if m.get('role')=='user']
  text_seen='DUO_NATIVE_ATTACHMENT_TEXT' in str(user_content)
  image_seen=any(isinstance(m,list) and any(b.get('type')=='image_url' and b.get('image_url',{}).get('url','').startswith('data:image/png;base64,') for b in m) for m in user_content)
  answer='DUO_ATTACHMENT_RESULT:text='+str(text_seen)+',image='+str(image_seen) if text_seen else 'DUO_HARNESS_API_OK'
  first={'id':'fixture','object':'chat.completion.chunk','created':1,'model':request['model'],'choices':[{'index':0,'delta':{'role':'assistant','content':answer},'finish_reason':None}]}
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
	discovered := discoverHarnessModels(ctx, env, req)
	if discovered.Status != "ready" || len(discovered.Models) != 2 {
		t.Fatalf("native WSL model discovery failed: %#v", discovered)
	}
	req.Models = discovered.Models
	old, err := readNativeAccountFiles(ctx, env, home, "deepseek-harness")
	if err != nil {
		t.Fatal(err)
	}
	next, err := mergeHarnessAPI(old, req)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("DUO_HARNESS_ATTACHMENT_NATIVE_FIXTURE") == "1" {
		vision := req
		vision.Model, vision.Input = "fixture-alternative", []string{"text", "image"}
		next, err = mergeHarnessAPI(next, vision)
		if err != nil {
			t.Fatal(err)
		}
		next, err = mergeHarnessAPI(next, req) // Restore the text default, retaining image metadata.
		if err != nil {
			t.Fatal(err)
		}
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
	catalog, err := modelsForEngine(ctx, env, "deepseek-harness", map[string]string{"DSH_HOME": home})
	if err != nil || catalog.Status != "ready" || len(catalog.Models) != 2 || catalog.DefaultModel != req.Model {
		t.Fatalf("WSL task catalog did not read saved models: %#v %v", catalog, err)
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
	expectedHits := 2
	if os.Getenv("DUO_HARNESS_ATTACHMENT_NATIVE_FIXTURE") == "1" {
		task.Session, task.Environment = session, &env
		textFiles := []RuntimeAttachment{{Attachment: Attachment{ID: "synthetic-text", Name: "history.md", Mime: "text/plain"}, Data: []byte("DUO_NATIVE_ATTACHMENT_TEXT\nUTF-8 中文记录")}}
		staged, cleanup, err := (&App{}).stageRuntimeFiles(ctx, task, textFiles)
		if err != nil {
			t.Fatal(err)
		}
		task.Files = staged
		continued, result, err := runHarnessACP(ctx, c, task, executionInput(task, "Read the attached text."), func(string, string) {})
		cleanup()
		if err != nil || continued != session || !strings.Contains(result, "text=True,image=False") {
			t.Fatalf("native text attachment admission failed: %v %s", err, result)
		}
		var picture bytes.Buffer
		if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
			t.Fatal(err)
		}
		task.Model = "fixture-alternative"
		imageFiles := []RuntimeAttachment{{Attachment: Attachment{ID: "synthetic-image", Name: "image.png", Mime: "image/png"}, Data: picture.Bytes()}}
		staged, cleanup, err = (&App{}).stageRuntimeFiles(ctx, task, imageFiles)
		if err != nil {
			t.Fatal(err)
		}
		task.Files = staged
		continued, result, err = runHarnessACP(ctx, c, task, executionInput(task, "Read the attached image."), func(string, string) {})
		cleanup()
		if err != nil || continued != session || !strings.Contains(result, "text=True,image=True") {
			t.Fatalf("native vision switch/image blocks/history failed: %v %s", err, result)
		}
		closeHarnessRuntimes()
		task.Files = nil
		continued, result, err = runHarnessACP(ctx, c, task, "Retain the previous attachments after closing.", func(string, string) {})
		if err != nil || continued != session || !strings.Contains(result, "text=True,image=True") {
			t.Fatalf("native image persistence after staging cleanup failed: %v %s", err, result)
		}
		textOnly := task
		textOnly.Model, textOnly.Files = req.Model, imageFiles
		if kept, _, err := runHarnessACP(ctx, c, textOnly, "Must reject after switching back to a text model.", func(string, string) {}); err == nil || kept != session || !strings.Contains(err.Error(), "未声明图片") {
			t.Fatal("native route validation lost image rejection guidance/session", err)
		}
		textOnly.Session = ""
		if _, _, err := runHarnessACP(ctx, c, textOnly, "Must reject before model invocation.", func(string, string) {}); err == nil || !strings.Contains(err.Error(), "未声明图片") {
			t.Fatal("native text-only model admitted image content", err)
		}
		expectedHits += 3
		t.Log("installed WSL Harness accepted staged UTF-8 text, reopened a text-only connection for a vision model with the same native session, sent real PNG image blocks, retained text/images after staging cleanup and cold resume, and rejected a text-only model without a provider request; isolated loopback only")
	}
	if os.Getenv("DUO_HARNESS_PERMISSION_NATIVE_FIXTURE") == "1" {
		for _, decision := range []string{"accept", "decline"} {
			approvalCount := 0
			permissionCtx := withCodexInteraction(ctx, func(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
				approvalCount++
				if method != harnessPermissionMethod {
					return nil, errors.New("unexpected native interaction")
				}
				request, err := parseHarnessPermission(params)
				if err != nil || request.ToolCall.Title != "bash" || !strings.Contains(string(request.ToolCall.RawInput), "DUO_PERMISSION_TOOL_EXECUTED") {
					return nil, errors.New("missing native command correlation")
				}
				return harnessAnswerPayload(params, CodexAnswer{Decision: decision})
			})
			permissionTask := Task{ID: "native-permission-" + decision, Engine: "deepseek-harness", Workspace: home, Model: req.Model, Mode: &WorkMode{ID: harnessWorkspaceMode, Permission: "workspace", Approval: "request", AllowNetwork: boolPtr(true)}}
			_, result, err := runHarnessACP(permissionCtx, c, permissionTask, "DUO_PERMISSION_PROBE "+decision, func(string, string) {})
			if err != nil || approvalCount != 1 || !strings.Contains(result, "DUO_PERMISSION_RESULT:") {
				t.Fatalf("native approval %s failed: requests=%d result=%s error=%v", decision, approvalCount, result, err)
			}
			if decision == "accept" && !strings.Contains(result, "DUO_PERMISSION_TOOL_EXECUTED") {
				t.Fatal("approved native command did not execute", result)
			}
			if decision == "decline" && !strings.Contains(strings.ToLower(result), "reject") {
				t.Fatal("native reject did not prevent execution", result)
			}
			closeHarnessRuntimes()
			expectedHits += 2
		}
		t.Log("official installed WSL Harness requested one-time approval with live tool details; accept executed the harmless marker and decline rejected it; loopback only")
	}
	if os.Getenv("DUO_HARNESS_MCP_NATIVE_FIXTURE") == "1" {
		task.Session, task.Files, task.Model = session, nil, req.Model
		for _, token := range []string{"synthetic-mcp-first", "synthetic-mcp-second"} {
			c.HardwareAI = &HardwareRuntime{URL: "http://127.0.0.1:" + strconv.Itoa(port) + "/mcp/hardware", Token: token}
			if token == "synthetic-mcp-first" {
				c.HardwareAI.FallbackURLs = []string{c.HardwareAI.URL}
				c.HardwareAI.URL = "http://127.0.0.1:1/mcp/hardware"
			}
			continued, result, err := runHarnessACP(ctx, c, task, "DUO_MCP_PROBE", func(string, string) {})
			if err != nil || continued != session || !strings.Contains(result, "DUO_MCP_AUTH:Bearer "+token) {
				t.Fatalf("native MCP did not use fresh authorization on the same session: %v %s", err, result)
			}
			expectedHits += 3 // Two provider requests and one synthetic MCP call.
		}
		c.HardwareAI = nil
		continued, result, err := runHarnessACP(ctx, c, task, "DUO_MCP_PROBE", func(string, string) {})
		if err != nil || continued != session || !strings.Contains(result, "DUO_MCP_UNMOUNTED") {
			t.Fatalf("native MCP remained mounted after removing grants: %v %s", err, result)
		}
		expectedHits++
		t.Log("official installed WSL Harness mounted HTTP MCP through ACP, executed only a synthetic read tool, renewed its token on the same durable session and removed tools after grants were cleared; isolated loopback only")
	}
	io.WriteString(in, "done\n")
	line, err = reader.ReadString('\n')
	if err != nil {
		t.Fatal("fixture summary missing")
	}
	var hits []struct {
		Model         string `json:"model"`
		Authenticated bool   `json:"authenticated"`
	}
	if json.Unmarshal([]byte(line), &hits) != nil || len(hits) != expectedHits || hits[0].Model != "model-list" || !hits[0].Authenticated || hits[1].Model != req.Model || !hits[1].Authenticated {
		t.Fatal("native credential or route was not used")
	}
	for _, hit := range hits {
		if !hit.Authenticated {
			t.Fatal("synthetic API authentication was lost")
		}
	}
	if err := server.Wait(); err != nil {
		t.Fatal("fixture server did not stop")
	}
	t.Log("selected WSL user authenticated model-list request, saved two native models and read both into Duo task catalog; installed dsh returned through Duo ACP; loopback only, zero paid calls")
}

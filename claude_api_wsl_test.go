package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Real WSL file/argument transport, isolated fake CLI, no model/network calls.
func TestClaudeScopedAPINativeWSLFilesAndInvocation(t *testing.T) {
	if os.Getenv("DUO_CLAUDE_WSL_SCOPE_FIXTURE") != "1" {
		t.Skip("explicit isolated WSL fixture only")
	}
	env := Environment{ID: "scoped-wsl", Name: "Synthetic WSL", Type: "wsl", Distro: os.Getenv("DUO_CLAUDE_WSL_DISTRO"), User: os.Getenv("DUO_CLAUDE_WSL_USER")}
	if env.Distro == "" {
		t.Fatal("provide exact WSL distribution")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := runtimeConfig(Config{}, env)
	setup := `import pathlib,tempfile,json
root=pathlib.Path(tempfile.mkdtemp(prefix='duo-claude-api-'))
root=root/"config 'quoted' $(literal)";root.mkdir()
(root/'settings.json').write_text('{"model":"native-unchanged","permissions":{"deny":["Read(private)"]}}')
(root/'.credentials.json').write_text('{"native_login":"synthetic-preserved"}')
cli=root/'mock-claude'
cli.write_text("#!/usr/bin/env python3\nimport sys,json,pathlib\na=sys.argv[1:]\np=a[a.index('--settings')+1]\nd=json.loads(pathlib.Path(p).read_text())\ns=a[a.index('--resume')+1]\nsys.stdin.read()\nresult={'role':d['env']['ANTHROPIC_DEFAULT_HAIKU_MODEL'],'model':a[a.index('--model')+1],'settings':p,'permission':a[a.index('--permission-mode')+1]}\nprint(json.dumps({'type':'result','session_id':s,'is_error':False,'result':json.dumps(result)}),flush=True)\n")
cli.chmod(0o700)
print(json.dumps({'root':str(root),'cli':str(cli)}))`
	cmd := commandWithContext(ctx, mimoTargetCommand(c, "python3", "-c", setup))
	raw, err := cmd.Output()
	var paths struct{ Root, CLI string }
	if err != nil || json.Unmarshal(raw, &paths) != nil || paths.Root == "" {
		t.Fatal("WSL fixture setup failed", err)
	}
	defer func() {
		script := `import pathlib,sys,tempfile,shutil
root=pathlib.Path(sys.argv[1]).resolve().parent
if root.parent!=pathlib.Path(tempfile.gettempdir()).resolve() or not root.name.startswith('duo-claude-api-'): raise ValueError('invalid cleanup root')
shutil.rmtree(root)`
		clean := commandWithContext(context.Background(), mimoTargetCommand(c, "python3", "-c", script, paths.Root))
		if err := clean.Run(); err != nil {
			t.Error("WSL fixture cleanup failed", err)
		}
	}()
	before, err := remoteNativeAccountFiles(ctx, env, nativeAccountRequest{Operation: "read", Engine: "claude", Directory: paths.Root})
	if err != nil {
		t.Fatal(err)
	}
	old, err := remoteNativeAccountFiles(ctx, env, nativeAccountRequest{Operation: "read", Engine: "claude-api", Directory: paths.Root})
	if err != nil {
		t.Fatal(err)
	}
	data := claudeAPISettings(EngineSetupRequest{Engine: "claude", APIKey: "synthetic-only", Model: "main", BaseURL: "https://synthetic.invalid", ClaudeRoles: &ClaudeRoleModels{Mode: "custom", Opus: "reasoner", Sonnet: "coder", Haiku: "fast"}})
	next := map[string][]byte{"duo-api.json": data}
	_, err = remoteNativeAccountFiles(ctx, env, nativeAccountRequest{Operation: "write", Engine: "claude-api", Directory: paths.Root, Expected: old, Files: next})
	if err != nil {
		t.Fatal(err)
	}
	after, err := remoteNativeAccountFiles(ctx, env, nativeAccountRequest{Operation: "read", Engine: "claude", Directory: paths.Root})
	if err != nil || string(before["settings.json"]) != string(after["settings.json"]) || string(before[".credentials.json"]) != string(after[".credentials.json"]) {
		t.Fatal("native WSL config changed", err)
	}
	env.ClaudeAPIConfig = paths.Root + "/duo-api.json"
	env.Workspaces = []string{paths.Root}
	env.Claude = paths.CLI
	list, err := configuredClaudeCatalog(ctx, env)
	if err != nil || list.DefaultModel != "main" || list.APITarget != hash(string(data)) {
		t.Fatal("WSL catalog/model scope incorrect", err)
	}
	c = runtimeConfig(Config{}, env)
	c.EngineEnv = map[string]string{"DUO_CLAUDE_SETTINGS": env.ClaudeAPIConfig}
	task := Task{Engine: "claude", Workspace: paths.Root, Model: "task-choice", Session: "existing-native-session", Mode: &WorkMode{Permission: "read"}}
	session, result, err := (CodexRunner{}).Run(ctx, c, task, "synthetic input", func(string, string) {})
	var got struct{ Role, Model, Settings, Permission string }
	json.Unmarshal([]byte(result), &got)
	if err != nil || session != task.Session || got.Role != "fast" || got.Model != "task-choice" || got.Settings != env.ClaudeAPIConfig || got.Permission != "plan" || strings.Contains(result, "synthetic-only") {
		t.Fatal("WSL session/model/settings/permission transport failed", err)
	}
	// A manually corrupted routing field must never export a credential.
	bad := claudeAPISettings(EngineSetupRequest{Engine: "claude", APIKey: "synthetic-only", Model: "main", BaseURL: "https://synthetic.invalid/synthetic-only"})
	_, err = remoteNativeAccountFiles(ctx, env, nativeAccountRequest{Operation: "write", Engine: "claude-api", Directory: paths.Root, Expected: next, Files: map[string][]byte{"duo-api.json": bad}})
	if err != nil {
		t.Fatal("isolated corruption fixture write failed", err)
	}
	stdout, _, err := runModelEnvironmentCommand(ctx, env, "python3", "-c", claudeScopedModelsScript, env.ClaudeAPIConfig)
	if err == nil || len(stdout) != 0 {
		t.Fatal("corrupted scoped catalog exported routing fields")
	}
	t.Log("real WSL isolated API file writes, original config preservation, role catalog and quoted settings path invocation passed; fake CLI and zero model calls")
}

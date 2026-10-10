package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Opt-in transport test with a synthetic Codex RPC peer, no real login or model.
func TestCodexAccountNativeWSLFixture(t *testing.T) {
	if os.Getenv("DUO_ACCOUNT_WSL_FIXTURE") != "1" {
		t.Skip("opt-in WSL account transport fixture")
	}
	env := Environment{ID: "synthetic-account", Type: "wsl", Distro: os.Getenv("DUO_ACCOUNT_WSL_DISTRO"), User: os.Getenv("DUO_ACCOUNT_WSL_USER")}
	if env.Distro == "" || env.User == "" {
		t.Fatal("exact WSL distro and user required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := commandWithContext(ctx, environmentProbeCommand(env, "mktemp", "-d", "/tmp/duo-account-native-XXXXXX"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("cannot create WSL fixture")
	}
	root := strings.TrimSpace(string(out))
	if !strings.HasPrefix(root, "/tmp/duo-account-native-") || strings.ContainsAny(root, "\r\n\x00") {
		t.Fatal("invalid fixture root")
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		cmd := commandWithContext(cleanupCtx, environmentProbeCommand(env, "python3", "-c", "import os,sys,shutil; p=sys.argv[1]; assert os.path.dirname(p)=='/tmp' and os.path.basename(p).startswith('duo-account-native-') and not os.path.islink(p); shutil.rmtree(p)", root))
		if cmd.Run() != nil {
			t.Error("fixture cleanup failed")
		}
	})
	// Same account home helper, redirecting only this fixture's Path.home.
	raw, _ := json.Marshal(map[string]any{"id": "fixture-account", "engine": "codex", "files": map[string][]byte{"config.toml": []byte("cli_auth_credentials_store=\"file\"\n")}})
	script := "import pathlib; pathlib.Path.home=classmethod(lambda cls:pathlib.Path(" + strconv.Quote(root) + "))\n" + managedAccountHomeScript
	cmd = commandWithContext(ctx, environmentProbeCommand(env, "sh", "-lc", "exec python3 -c "+posixQuote(script)))
	cmd.Stdin = bytes.NewReader(raw)
	out, err = cmd.Output()
	if err != nil {
		t.Fatal("remote account directory helper failed")
	}
	var homeResult struct {
		Directory string `json:"directory"`
	}
	if json.Unmarshal(out, &homeResult) != nil {
		t.Fatal("invalid helper reply")
	}
	home := homeResult.Directory
	if home != root+"/.local/share/duo/engine-accounts/fixture-account" {
		t.Fatal("wrong account home")
	}
	const peer = `#!/usr/bin/python3
import json,os,pathlib,sys,time
home=pathlib.Path(os.environ['CODEX_HOME'])
assert str(home).startswith('/tmp/duo-account-native-')
assert all(k not in os.environ for k in ('OPENAI_API_KEY','CODEX_API_KEY','OPENAI_BASE_URL'))
assert json.loads(sys.stdin.readline())['method']=='initialize'
print(json.dumps({'id':'setup-init','result':{}}),flush=True)
assert json.loads(sys.stdin.readline())['method']=='initialized'
request=json.loads(sys.stdin.readline()); assert request['method']=='account/login/start'
p=request['params']
if p['type']=='apiKey':
 assert p['apiKey']=='synthetic'
 (home/'auth.json').write_text(json.dumps({'OPENAI_API_KEY':'synthetic'}))
 print(json.dumps({'id':'setup-login','result':{'type':'apiKey'}}),flush=True)
else:
 print(json.dumps({'id':'setup-login','result':{'type':'chatgptDeviceCode','loginId':'fixture','verificationUrl':'https://auth.openai.com/codex/device','userCode':'FIXTURE'}}),flush=True)
 if (home/'cancel').exists():
  (home/'peer.pid').write_text(str(os.getpid()))
  time.sleep(30)
 else:
  print(json.dumps({'method':'account/login/completed','params':{'loginId':'fixture','success':True}}),flush=True)
assert sys.stdin.read()==''
`
	env.Codex = root + "/fixture-codex"
	write := commandWithContext(ctx, environmentProbeCommand(env, "python3", "-c", "import pathlib,sys; p=pathlib.Path(sys.argv[1]); p.write_text(sys.stdin.read()); p.chmod(0o700)", env.Codex))
	write.Stdin = strings.NewReader(peer)
	if write.Run() != nil {
		t.Fatal("cannot write synthetic peer")
	}
	for _, login := range []string{"apiKey", "chatgptDeviceCode"} {
		calls := 0
		err = setupCodexAccount(ctx, env, home, login, "synthetic", func(link, code string) {
			calls++
			if code != "FIXTURE" {
				t.Error("lost device code")
			}
		})
		if err != nil || (calls == 1) != (login == "chatgptDeviceCode") {
			t.Fatal("WSL account protocol failed", err, calls)
		}
	}
	mark := commandWithContext(ctx, environmentProbeCommand(env, "touch", home+"/cancel"))
	if mark.Run() != nil {
		t.Fatal("cannot prepare cancellation")
	}
	cancelCtx, stop := context.WithCancel(ctx)
	err = setupCodexAccount(cancelCtx, env, home, "chatgptDeviceCode", "", func(string, string) { stop() })
	stop()
	if err == nil {
		t.Fatal("canceled login reported success")
	}
	check := commandWithContext(ctx, environmentProbeCommand(env, "python3", "-c", `import os,pathlib,sys
p=int((pathlib.Path(sys.argv[1])/'peer.pid').read_text())
try: os.kill(p,0)
except ProcessLookupError: sys.exit(0)
sys.exit(1)`, home))
	if check.Run() != nil {
		t.Fatal("canceled login left a writer process")
	}
	t.Log("WSL selected-user account home, API and device-code RPC, and canceled process cleanup passed with synthetic credentials")
}

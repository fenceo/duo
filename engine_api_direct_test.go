package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvironmentAPIConfigAppliesWithoutCreatingAccount(t *testing.T) {
	for _, engine := range []string{"claude", "mimo"} {
		t.Run(engine, func(t *testing.T) {
			a := fixture(t, &fakeRunner{})
			c := a.config.get()
			dir := t.TempDir()
			c.Environments = []Environment{{ID: "local", Name: "Synthetic", Type: "windows", Codex: "codex.exe", Workspaces: []string{dir}}}
			c.DefaultEnvironment = "local"
			if err := a.config.save(c); err != nil {
				t.Fatal(err)
			}
			env, _ := a.config.get().environment("local")
			if engine == "claude" {
				if err := a.store.useSyncedNativeAccount(env.ID, engine, "old-source"); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := env
			a.engineSetup.apiDirectory = func(context.Context, Environment) (string, error) { return dir, nil }
			files := map[string][]byte{"duo-api.json": []byte(`{"permission":{"edit":"ask"},"provider":{"keep":{"name":"Keep"}}}`)}
			if engine == "claude" {
				files = map[string][]byte{".credentials.json": []byte(`{"claudeAiOauth":{"accessToken":"synthetic-old-login"}}`), "settings.json": []byte(`{"permissions":{"deny":["Read(secret)"]},"mcpServers":{"keep":{}}}`)}
			}
			reads, writes, probes := 0, 0, 0
			a.engineSetup.harnessRead = func(_ context.Context, target Environment, _, kind string) (map[string][]byte, error) {
				reads++
				if target.ID != env.ID || kind != map[string]string{"claude": "claude", "mimo": "mimo-api"}[engine] {
					t.Fatal("wrong target")
				}
				return files, nil
			}
			a.engineSetup.harnessWrite = func(_ context.Context, _ Environment, _, _ string, _, next map[string][]byte) error {
				writes++
				files = next
				return nil
			}
			a.engineSetup.harnessDiscover = func(_ context.Context, _ Environment, v HarnessAPIRequest) HarnessDiscovery {
				probes++
				if v.APIKey != "synthetic-direct-key" {
					t.Fatal("key missing or wrong")
				}
				return HarnessDiscovery{Status: "ready", Models: []ModelOption{{ID: "fixture-model"}, {ID: "another-model"}}, Message: "Fixture GET only"}
			}
			request := toolsClient(t, a)
			path := "/api/environments/local/engines/" + engine + "/api"
			var view EngineAPIView
			if err := json.Unmarshal(request(path, "GET", nil, 200), &view); err != nil {
				t.Fatal(err)
			}
			v := EngineAPIRequest{Target: view.Target, Engine: engine, APIProtocol: "openai-chat", BaseURL: "https://relay.example/v1", Model: "fixture-model", APIKey: "synthetic-direct-key"}
			if engine == "claude" {
				v.APIProtocol = "anthropic-messages"
				v.AuthMode = "auth-token"
			}
			body := request(path, "PUT", v, 200)
			if strings.Contains(string(body), v.APIKey) || strings.Contains(string(body), "synthetic-old-login") || len(a.store.engineProfiles()) != 0 || writes != 1 || probes != 1 {
				t.Fatal("direct save leaked a key or created inventory")
			}
			var saved EngineAPIView
			_ = json.Unmarshal(body, &saved)
			if !saved.KeyConfigured || saved.Target == v.Target {
				t.Fatal("saved view missing key flag or new target")
			}
			if engine == "claude" && a.store.currentEnvironmentAccount(env.ID, engine) != "" {
				t.Fatal("replaced API still claims old account inventory")
			}
			if engine == "claude" {
				if files[".credentials.json"] != nil || !strings.Contains(string(files["settings.json"]), "Read(secret)") || !strings.Contains(string(files["settings.json"]), "mcpServers") {
					t.Fatal("permissions lost or stale subscription retained")
				}
			} else {
				if !strings.Contains(string(files["duo-api.json"]), "another-model") {
					t.Fatal("validated models missing from native CLI configuration")
				}
				if !strings.Contains(string(files["duo-api.json"]), `"edit": "ask"`) || !strings.Contains(string(files["duo-api.json"]), `"keep"`) {
					t.Fatal("native preferences lost")
				}
				actual := a.activeEngineEnvironment(Task{Engine: "mimo", Environment: &snapshot})
				if actual["MIMOCODE_CONFIG"] != filepath.Join(dir, "duo-api.json") || actual["MIMOCODE_HOME"] != "" {
					t.Fatal("existing tasks did not pick up config or history relocated")
				}
			}
			v.Target = saved.Target
			v.APIKey = ""
			request(path+"/models", "POST", v, 200)
			if writes != 1 || probes != 2 {
				t.Fatal("model listing changed files")
			}
			v.BaseURL = "https://other.example/v1"
			request(path+"/models", "POST", v, 400)
			if probes != 2 {
				t.Fatal("saved key sent to changed service")
			}
			v.Target = "stale"
			request(path, "PUT", v, 409)
			if writes != 1 {
				t.Fatal("stale environment modified")
			}
			v.Target = saved.Target
			a.engineSetup.active = true
			request(path, "PUT", v, 409)
			a.engineSetup.active = false
			if reads != 4 {
				t.Fatal("busy/stale requests should fail before reading native credentials", reads)
			}
		})
	}
}

func TestEnvironmentAPIFailureRollsBackFilesAndKeepsLegacyReferences(t *testing.T) {
	a, env := engineSetupFixture(t)
	dir := t.TempDir()
	p := EngineCredentialProfile{ID: "existing-mimo", Name: "Existing", Engine: "mimo", EnvironmentID: env.ID, Kind: "mimo_config", Reference: dir}
	if err := a.store.saveEngineProfiles([]EngineCredentialProfile{p}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.activateEngineProfile(env.ID, "mimo", p.ID); err != nil {
		t.Fatal(err)
	}
	writes := 0
	old := map[string][]byte{"mimocode.json": []byte(`{"permission":{"shell":"ask"}}`)}
	var last map[string][]byte
	a.engineSetup.harnessRead = func(_ context.Context, _ Environment, directory, kind string) (map[string][]byte, error) {
		if directory != dir || kind != "mimo" {
			t.Fatal("legacy API configuration not reused")
		}
		return old, nil
	}
	a.engineSetup.harnessWrite = func(_ context.Context, _ Environment, _, _ string, _, next map[string][]byte) error {
		writes++
		last = next
		return nil
	}
	a.engineSetup.harnessDiscover = syntheticHarnessDiscovery
	a.config.path = filepath.Join(t.TempDir(), "missing-parent", "config.json")
	v := EngineAPIRequest{Target: a.engineAPITarget(env, "mimo"), APIProtocol: "openai-chat", BaseURL: "https://api.example/v1", Model: "fixture-model", APIKey: "synthetic-direct-key"}
	toolsClient(t, a)("/api/environments/"+env.ID+"/engines/mimo/api", "PUT", v, 500)
	if writes != 2 || string(last["mimocode.json"]) != string(old["mimocode.json"]) || a.store.activeEngineProfile(env.ID, "mimo") != p.ID || len(a.store.engineProfiles()) != 1 {
		t.Fatal("rollback lost files or existing inventory")
	}
	if err := a.store.activateEngineProfile(env.ID, "mimo", ""); err != nil {
		t.Fatal(err)
	}
	toolsClient(t, a)("/api/environments/"+env.ID+"/engines/mimo/api", "PUT", v, 409)
	if writes != 2 {
		t.Fatal("stale active profile was modified")
	}
}

func TestMimoLinuxArchiveAcceptsOnlyBinaryAndAppleMetadata(t *testing.T) {
	python, err := exec.LookPath("python")
	if err != nil {
		python, err = exec.LookPath("python3")
	}
	if err != nil {
		t.Skip("Python unavailable")
	}
	check := `
import tempfile,io,tarfile,pathlib
cases=[('binary', [('mimo','file',b'fixture')],True), ('metadata',[('._mimo','file',b'\x00\x05\x16\x07'+b'fixture'),('mimo','file',b'fixture')],True), ('invalid-metadata',[('._mimo','file',b'not-valid'),('mimo','file',b'fixture')],False), ('extra',[('mimo','file',b'fixture'),('other','file',b'fixture')],False), ('traversal',[('../mimo','file',b'fixture')],False), ('symlink',[('mimo','symlink',b'')],False), ('duplicate',[('mimo','file',b'fixture'),('./mimo','file',b'fixture')],False)]
for label,members,expected in cases:
 with tempfile.TemporaryDirectory() as d:
  root=pathlib.Path(d);archive=root/'fixture.tar.gz'
  with tarfile.open(archive,'w:gz') as package:
   for name,kind,data in members:
    info=tarfile.TarInfo(name);info.size=len(data)
    if kind=='symlink': info.type=tarfile.SYMTYPE;info.linkname='../outside'
    package.addfile(info,io.BytesIO(data))
  accepted=True
  try: extract_binary(archive,root)
  except ValueError: accepted=False
  assert accepted==expected,label
  assert (root/'mimo').exists()==expected,label
print('PASS: Linux metadata accepted; extra files, unsafe paths, links and duplicates rejected')
`
	encoded, _ := json.Marshal(check)
	cmd := exec.Command(python, "-c", "import sys;ns={'__name__':'fixture'};exec(sys.stdin.read(),ns);exec("+string(encoded)+",ns)")
	cmd.Stdin = strings.NewReader(remoteMimoInstall)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	t.Log(string(out))
}

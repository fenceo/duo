package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func claudeHardwareArgs(h *HardwareRuntime) []string {
	raw, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"jianzuo_hardware": map[string]any{"type": "http", "url": h.URL, "headers": map[string]string{"Authorization": "Bearer ${JIANZUO_HARDWARE_TOKEN}"}}}})
	return []string{"--mcp-config", string(raw), "--allowedTools", "mcp__jianzuo_hardware__*"}
}
func probeHardwareMCP(ctx context.Context, h *HardwareRuntime) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", h.URL, strings.NewReader(`{"jsonrpc":"2.0","id":"probe","method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"jianzuo-probe","version":"1"}}}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+h.Token)
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("硬件工具连接失败，请检查执行环境到简作的网络：%v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("硬件工具连接失败：HTTP %d", response.StatusCode)
	}
	var v struct {
		Result struct {
			Info struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if json.NewDecoder(response.Body).Decode(&v) != nil || v.Result.Info.Name != "jianzuo-hardware" {
		return fmt.Errorf("硬件工具地址未指向简作服务")
	}
	return nil
}

// Pass credentials through stdin and the child environment, never command-line
// arguments, shell interpolation, global CLI config, or the user's prompt.
const hardwareLauncher = `import sys,subprocess,json,os,urllib.request
payload=json.loads(sys.stdin.buffer.readline())
prompt=sys.stdin.buffer.read()
print(json.dumps({'type':'jianzuo.process','pid':os.getpid()}),flush=True)
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kwargs): return None
client=urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect())
message={'jsonrpc':'2.0','id':'probe','method':'initialize','params':{'protocolVersion':'2025-06-18','capabilities':{},'clientInfo':{'name':'jianzuo-probe','version':'1'}}}
selected=None;failures=[]
for address in [payload['url']]+(payload.get('fallback_urls') or []):
 try:
  request=urllib.request.Request(address,data=json.dumps(message).encode(),headers={'Content-Type':'application/json','Accept':'application/json, text/event-stream','Authorization':'Bearer '+payload['token']})
  with client.open(request,timeout=4) as response: result=json.load(response)
  if result.get('result',{}).get('serverInfo',{}).get('name')!='jianzuo-hardware':raise ValueError('not a Jianzuo hardware endpoint')
  selected=address;break
 except Exception as error: failures.append(address+': '+str(error))
if selected is None:
 print('Hardware MCP connection failed. Tried addresses: '+'; '.join(failures),file=sys.stderr)
 sys.exit(1)
args=sys.argv[2:]
for i,arg in enumerate(args):
 if arg.startswith('mcp_servers.jianzuo_hardware.url='):
  args[i]='mcp_servers.jianzuo_hardware.url='+json.dumps(selected)
 if arg=='--mcp-config' and i+1<len(args):
  config=json.loads(args[i+1]);config['mcpServers']['jianzuo_hardware']['url']=selected
  args[i+1]=json.dumps(config)
print(json.dumps({'type':'jianzuo.hardware','url':selected}),flush=True)
env=os.environ.copy();env['JIANZUO_HARDWARE_TOKEN']=payload['token']
os.chdir(sys.argv[1])
p=subprocess.Popen(args,start_new_session=True,stdin=subprocess.PIPE,env=env)
print(json.dumps({'type':'jianzuo.process','pid':p.pid}),flush=True)
p.communicate(input=prompt)
sys.exit(p.returncode)
`

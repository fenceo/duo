package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Resolve from the execution environment, not from Windows. Credentials only
// travel through stdin and ACP; they never enter argv or a native config file.
const harnessHardwareProbe = `import sys,json,urllib.request
cfg=json.load(sys.stdin)
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*args,**kwargs): return None
client=urllib.request.build_opener(urllib.request.ProxyHandler({}),NoRedirect())
message={'jsonrpc':'2.0','id':'probe','method':'initialize','params':{'protocolVersion':'2025-06-18','capabilities':{},'clientInfo':{'name':'duo-harness-probe','version':'1'}}}
for address in cfg['urls']:
 try:
  request=urllib.request.Request(address,data=json.dumps(message).encode(),headers={'Content-Type':'application/json','Accept':'application/json, text/event-stream','Authorization':'Bearer '+cfg['token']})
  with client.open(request,timeout=4) as response: result=json.load(response)
  if result.get('result',{}).get('serverInfo',{}).get('name')!='jianzuo-hardware': continue
  print(json.dumps({'url':address}),flush=True)
  sys.exit(0)
 except Exception: pass
sys.exit(1)
`

func resolveHarnessHardware(ctx context.Context, c Config) (*HardwareRuntime, error) {
	h := c.HardwareAI
	if h == nil {
		return nil, nil
	}
	if c.Distro == "" && c.SSHHost == "" {
		if err := probeHardwareMCP(ctx, h); err != nil {
			return nil, err
		}
		return &HardwareRuntime{URL: h.URL, Token: h.Token}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	urls := append([]string{h.URL}, h.FallbackURLs...)
	payload, _ := json.Marshal(map[string]any{"urls": urls, "token": h.Token})
	cmd := commandWithContext(ctx, command(c, "python3", "-c", harnessHardwareProbe))
	hideCommand(cmd)
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.Output()
	var selected struct {
		URL string `json:"url"`
	}
	if err == nil && json.Unmarshal(out, &selected) == nil {
		for _, address := range urls {
			if selected.URL == address {
				return &HardwareRuntime{URL: address, Token: h.Token}, nil
			}
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, errors.New("Harness 无法从执行环境连接 Duo 桌面/硬件工具；请检查 Python 3、网络及设置中的局域网或 Tailscale 访问地址（未发送模型请求）")
}

func harnessMCPServers(h *HardwareRuntime) []any {
	servers := []any{}
	if h != nil {
		servers = append(servers, map[string]any{
			"type": "http", "name": "duo_hardware", "url": h.URL,
			"headers": []any{map[string]string{"name": "Authorization", "value": "Bearer " + h.Token}},
		})
	}
	return servers
}

package main

import (
	"errors"
	"net"
	"net/url"
	"strings"
)

type AccessConfig struct {
	LAN       string `json:"lan"`
	Tailscale string `json:"tailscale"`
}

func normalizeAccess(a *AccessConfig) error {
	for _, value := range []*string{&a.LAN, &a.Tailscale} {
		*value = strings.TrimSpace(*value)
		if *value == "" {
			continue
		}
		u, e := url.Parse(*value)
		if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || len(*value) > 1024 || strings.ContainsAny(*value, "\r\n\\") {
			return errors.New("访问地址应为 http://主机:端口 或 https://域名，不包含账号、路径、参数或密码")
		}
		u.Path = ""
		*value = u.String()
	}
	return nil
}

// Settings may only flip the bind host between loopback and all interfaces.
// Port and other listen shapes stay in config.json after an intentional restart.
func applyListenAccess(current, requested string) (string, error) {
	current = strings.TrimSpace(current)
	requested = strings.TrimSpace(requested)
	if requested == "" || requested == current {
		return current, nil
	}
	_, port, err := net.SplitHostPort(current)
	if err != nil || port == "" {
		return "", errors.New("当前监听地址无效，无法切换远程访问")
	}
	host, reqPort, err := net.SplitHostPort(requested)
	if err != nil || reqPort != port {
		return "", errors.New("不能在此更改监听端口；请退出后修改 config.json 的 listen")
	}
	switch host {
	case "127.0.0.1", "0.0.0.0":
		return net.JoinHostPort(host, port), nil
	default:
		return "", errors.New("监听主机仅支持 127.0.0.1（仅本机）或 0.0.0.0（局域网与 Tailscale）")
	}
}

func listenAllowsRemote(listen string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(listen))
	return err == nil && host != "" && host != "127.0.0.1" && host != "localhost" && host != "::1"
}

func (f *Feishu) panelLinks(task string, view ...string) []any {
	a := f.app.config.get().Access
	if normalizeAccess(&a) != nil {
		return nil
	}
	buttons := []any{}
	for _, v := range []struct{ label, base string }{{"局域网", a.LAN}, {"远程", a.Tailscale}} {
		if v.base == "" {
			continue
		}
		u, _ := url.Parse(v.base)
		u.Path = "/"
		if task != "" {
			q := url.Values{}
			q.Set("task", task)
			if len(view) > 0 && view[0] == "note" {
				q.Set("view", "note")
			}
			u.RawQuery = q.Encode()
		}
		buttons = append(buttons, map[string]any{"tag": "button", "text": plainCardText(v.label), "type": "text", "size": "small", "width": "default", "url": u.String()})
	}
	if len(buttons) == 0 {
		return nil
	}
	return []any{map[string]any{"tag": "action", "actions": buttons}}
}

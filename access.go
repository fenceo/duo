package main

import (
	"errors"
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

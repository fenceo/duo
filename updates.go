package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The public upstream repository used as the default update source.
var releaseRepository = "fenceo/jianzuo"

const portableAssetName = "Jianzuo-portable-windows-x64.zip"

type UpdateInfo struct {
	Current          string `json:"current"`
	Repository       string `json:"repository"`
	State            string `json:"state"`
	Message          string `json:"message"`
	Latest           string `json:"latest,omitempty"`
	Published        string `json:"published,omitempty"`
	Checked          int64  `json:"checked,omitempty"`
	Notes            string `json:"notes,omitempty"`
	ReleasesURL      string `json:"releases_url,omitempty"`
	ReleaseURL       string `json:"release_url,omitempty"`
	DownloadURL      string `json:"download_url,omitempty"`
	ChecksumURL      string `json:"checksum_url,omitempty"`
	Digest           string `json:"digest,omitempty"`
	Size             int64  `json:"size,omitempty"`
	InstallSupported bool   `json:"install_supported"`
	InstallMessage   string `json:"install_message,omitempty"`
}
type UpdateChecker struct {
	mu             sync.Mutex
	client         *http.Client
	downloadClient *http.Client
	cached         UpdateInfo
	cachedErr      error
}

func newUpdateChecker() *UpdateChecker {
	return &UpdateChecker{
		client: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("GitHub 仓库地址发生重定向，请检查更新源")
		}},
		downloadClient: newUpdateDownloadClient(),
	}
}

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_.-]{1,100}$`)

func normalizeRepository(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "https://github.com/") {
		u, e := url.Parse(value)
		if e != nil || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return "", errors.New("填写 GitHub 的 用户名/仓库名 或仓库主页链接")
		}
		value = strings.Trim(u.Path, "/")
	}
	value = strings.TrimSuffix(value, ".git")
	if value != "" && (!repositoryPattern.MatchString(value) || strings.HasSuffix(value, "/.") || strings.HasSuffix(value, "/..")) {
		return "", errors.New("填写 GitHub 的 用户名/仓库名，例如 owner/jianzuo")
	}
	return value, nil
}
func (s *Store) updateRepository() string {
	if value := s.setting("update_repository"); value != "" {
		return value
	}
	return releaseRepository
}
func updateBase(repo string) UpdateInfo {
	v := UpdateInfo{Current: version, Repository: repo, State: "unchecked", Message: "点击检查更新，获取 GitHub 最新正式版本。"}
	if repo == "" {
		v.State = "unconfigured"
		v.Message = "先填写发布简作版本的 GitHub 仓库。"
	} else {
		v.ReleasesURL = "https://github.com/" + repo + "/releases"
	}
	return v
}

var stableVersionPattern = regexp.MustCompile(`^v?([0-9]{1,9})\.([0-9]{1,9})\.([0-9]{1,9})$`)

func compareReleaseVersions(remote, local string) (int, error) {
	parse := func(v string) ([3]uint64, error) {
		var out [3]uint64
		parts := stableVersionPattern.FindStringSubmatch(v)
		if parts == nil {
			return out, errors.New("版本号需为 v主版本.次版本.修订版本")
		}
		for i := 0; i < 3; i++ {
			out[i], _ = strconv.ParseUint(parts[i+1], 10, 64)
		}
		return out, nil
	}
	a, e := parse(remote)
	if e != nil {
		return 0, e
	}
	b, e := parse(strings.TrimSuffix(local, "-portable"))
	if e != nil {
		return 0, e
	}
	for i := range a {
		if a[i] > b[i] {
			return 1, nil
		}
		if a[i] < b[i] {
			return -1, nil
		}
	}
	return 0, nil
}
func releaseLink(raw, repo, path string) string {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	prefix := "/" + repo
	if len(u.Path) < len(prefix) || !strings.EqualFold(u.Path[:len(prefix)], prefix) || u.Path[len(prefix):] != path {
		return ""
	}
	return u.String()
}
func (c *UpdateChecker) snapshot(repo string) UpdateInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached.Repository == repo && c.cached.Checked != 0 && c.cachedErr == nil {
		return c.cached
	}
	return updateBase(repo)
}
func (c *UpdateChecker) check(ctx context.Context, repo string) (UpdateInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cached.Repository == repo && c.cached.Checked > 0 && time.Since(time.UnixMilli(c.cached.Checked)) < 30*time.Second {
		return c.cached, c.cachedErr
	}
	result, err := c.fetch(ctx, repo)
	result.Checked = now()
	c.cached = result
	c.cachedErr = err
	return result, err
}
func (c *UpdateChecker) fetch(ctx context.Context, repo string) (UpdateInfo, error) {
	v := updateBase(repo)
	normalized, err := normalizeRepository(repo)
	if err != nil || normalized != repo {
		return v, errors.New("更新仓库地址无效")
	}
	if repo == "" {
		return v, nil
	}
	request, err := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return v, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "Jianzuo/"+version)
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	response, err := c.client.Do(request)
	if err != nil {
		return v, errors.New("无法连接 GitHub，请检查运行简作电脑的网络后重试")
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		v.State = "no_release"
		v.Message = "尚无公开的正式版本，或仓库地址不正确。"
		return v, nil
	}
	if response.StatusCode == 403 || response.StatusCode == 429 {
		return v, errors.New("GitHub 暂时限制访问，请稍后再检查；也可打开版本页面")
	}
	if response.StatusCode != 200 {
		return v, fmt.Errorf("GitHub 返回 HTTP %d，请稍后重试", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil || len(raw) > 2*1024*1024 {
		return v, errors.New("GitHub 版本信息读取失败或内容过大")
	}
	var release struct {
		Tag        string `json:"tag_name"`
		Body       string `json:"body"`
		URL        string `json:"html_url"`
		Published  string `json:"published_at"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name   string `json:"name"`
			State  string `json:"state"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"assets"`
	}
	if json.Unmarshal(raw, &release) != nil {
		return v, errors.New("GitHub 版本信息格式异常")
	}
	if release.Draft || release.Prerelease {
		v.State = "no_release"
		v.Message = "尚无正式版本，预发布版本不会作为更新推荐。"
		return v, nil
	}
	comparison, err := compareReleaseVersions(release.Tag, version)
	if err != nil {
		return v, err
	}
	v.ReleaseURL = releaseLink(release.URL, repo, "/releases/tag/"+release.Tag)
	if v.ReleaseURL == "" {
		return v, errors.New("GitHub 版本链接与更新仓库不匹配")
	}
	v.Latest = release.Tag
	v.Published = release.Published
	v.Notes = release.Body
	if len([]rune(v.Notes)) > 12000 {
		v.Notes = string([]rune(v.Notes)[:12000]) + "\n…"
	}
	v.State = "current"
	v.Message = "当前已是最新正式版本。"
	if comparison > 0 {
		v.State = "available"
		v.Message = "发现新版本，可以下载便携包。"
	} else if comparison < 0 {
		v.State = "ahead"
		v.Message = "本机版本高于仓库最新正式版本。"
	}
	for _, a := range release.Assets {
		if a.State != "uploaded" || a.Size <= 0 {
			continue
		}
		link := releaseLink(a.URL, repo, "/releases/download/"+release.Tag+"/"+a.Name)
		if link == "" {
			continue
		}
		if a.Name == portableAssetName {
			v.DownloadURL = link
			v.Size = a.Size
			if regexp.MustCompile(`^sha256:[a-fA-F0-9]{64}$`).MatchString(a.Digest) {
				v.Digest = a.Digest
			}
		}
		if a.Name == portableAssetName+".sha256" {
			v.ChecksumURL = link
		}
	}
	if v.State == "available" && v.DownloadURL == "" {
		v.Message = "发现新版本，便携包尚未上传，可先查看更新说明。"
	}
	return v, nil
}
func (s *Server) updateRoutes(m *http.ServeMux) {
	checker := newUpdateChecker()
	m.HandleFunc("GET /api/updates", s.secure(func(w http.ResponseWriter, r *http.Request) {
		jsonOut(w, 200, s.decorateUpdate(checker.snapshot(s.app.store.updateRepository())))
	}))
	m.HandleFunc("PUT /api/updates", s.secure(func(w http.ResponseWriter, r *http.Request) {
		var v struct {
			Repository string `json:"repository"`
		}
		if !body(w, r, &v) {
			return
		}
		repo, err := normalizeRepository(v.Repository)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		if err = s.app.store.set("update_repository", repo); err != nil {
			fail(w, 500, err.Error())
			return
		}
		jsonOut(w, 200, s.decorateUpdate(checker.snapshot(s.app.store.updateRepository())))
	}))
	m.HandleFunc("POST /api/updates/check", s.secure(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 13*time.Second)
		defer cancel()
		v, err := checker.check(ctx, s.app.store.updateRepository())
		if err != nil {
			fail(w, 502, err.Error())
			return
		}
		jsonOut(w, 200, s.decorateUpdate(v))
	}))
	m.HandleFunc("POST /api/updates/install", s.secure(func(w http.ResponseWriter, r *http.Request) {
		result, err := s.installUpdate(r.Context(), checker)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		jsonOut(w, 202, result)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if s.shutdown != nil {
			go func() {
				time.Sleep(700 * time.Millisecond)
				s.shutdown()
			}()
		}
	}))
}

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
var releaseRepository = "fenceo/duo"

const portableAssetName = "Duo-portable-windows-x64.zip"
const installerAssetName = "Duo-Setup-User-x64.exe"

type updateAsset struct {
	URL, ChecksumURL, Digest string
	Size                     int64
}

type UpdateInfo struct {
	Current              string `json:"current"`
	Repository           string `json:"repository"`
	State                string `json:"state"`
	Message              string `json:"message"`
	Latest               string `json:"latest,omitempty"`
	Published            string `json:"published,omitempty"`
	Checked              int64  `json:"checked,omitempty"`
	Notes                string `json:"notes,omitempty"`
	ReleasesURL          string `json:"releases_url,omitempty"`
	ReleaseURL           string `json:"release_url,omitempty"`
	DownloadURL          string `json:"download_url,omitempty"`
	ChecksumURL          string `json:"checksum_url,omitempty"`
	Digest               string `json:"digest,omitempty"`
	Size                 int64  `json:"size,omitempty"`
	InstallSupported     bool   `json:"install_supported"`
	InstallMessage       string `json:"install_message,omitempty"`
	InstallationMode     string `json:"installation_mode"`
	PackageKind          string `json:"package_kind,omitempty"`
	InstallerDownloadURL string `json:"installer_download_url,omitempty"`
	InstallerChecksumURL string `json:"installer_checksum_url,omitempty"`
	PortableDownloadURL  string `json:"portable_download_url,omitempty"`
	PortableChecksumURL  string `json:"portable_checksum_url,omitempty"`
	installer            updateAsset
	portable             updateAsset
}
type UpdateChecker struct {
	mu             sync.Mutex
	client         *http.Client
	pageClient     *http.Client
	downloadClient *http.Client
	cached         UpdateInfo
	cachedErr      error
}

func newUpdateChecker() *UpdateChecker {
	return &UpdateChecker{
		client: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("GitHub 仓库地址发生重定向，请检查更新源")
		}},
		pageClient: &http.Client{Timeout: 12 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || req.URL.Scheme != "https" || req.URL.Host != "github.com" || req.URL.User != nil {
				return errors.New("GitHub 版本页面发生了不安全的重定向")
			}
			return nil
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
		if strings.EqualFold(value, "fenceo/jianzuo") {
			return "fenceo/duo"
		}
		return value
	}
	return releaseRepository
}
func updateBase(repo string) UpdateInfo {
	v := UpdateInfo{Current: version, Repository: repo, State: "unchecked", Message: "点击检查更新，获取 GitHub 最新正式版本。"}
	if repo == "" {
		v.State = "unconfigured"
		v.Message = "先填写发布Duo版本的 GitHub 仓库。"
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
	request.Header.Set("User-Agent", "Duo/"+version)
	// Keep this on a published GitHub API version. An unknown/future value can
	// be rejected by proxies even when the endpoint itself is reachable.
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := c.client.Do(request)
	if err != nil {
		if fallback, fallbackErr := c.fetchReleasePage(ctx, repo); fallbackErr == nil {
			return fallback, nil
		}
		return v, errors.New("无法连接 GitHub，请检查运行Duo电脑的网络后重试")
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		v.State = "no_release"
		v.Message = "尚无公开的正式版本，或仓库地址不正确。"
		return v, nil
	}
	if response.StatusCode == 403 || response.StatusCode == 429 {
		if fallback, fallbackErr := c.fetchReleasePage(ctx, repo); fallbackErr == nil {
			return fallback, nil
		}
		return v, githubRateLimitError(response)
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
		v.Message = "发现新版本，可以查看更新说明。"
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
		var asset *updateAsset
		switch a.Name {
		case portableAssetName, portableAssetName + ".sha256":
			asset = &v.portable
		case installerAssetName, installerAssetName + ".sha256":
			asset = &v.installer
		default:
			continue
		}
		if !strings.HasSuffix(a.Name, ".sha256") {
			asset.URL = link
			asset.Size = a.Size
			if regexp.MustCompile(`^sha256:[a-fA-F0-9]{64}$`).MatchString(a.Digest) {
				asset.Digest = a.Digest
			}
		} else {
			asset.ChecksumURL = link
		}
	}
	v.InstallerDownloadURL, v.InstallerChecksumURL = v.installer.URL, v.installer.ChecksumURL
	v.PortableDownloadURL, v.PortableChecksumURL = v.portable.URL, v.portable.ChecksumURL
	// Keep the old checker contract for callers; decoration selects the installed asset.
	v.DownloadURL, v.ChecksumURL, v.Digest, v.Size = v.portable.URL, v.portable.ChecksumURL, v.portable.Digest, v.portable.Size
	if v.State == "available" && v.DownloadURL == "" {
		v.Message = "发现新版本，便携包尚未上传，可先查看更新说明。"
	}
	return v, nil
}

func githubRateLimitError(response *http.Response) error {
	if response == nil {
		return errors.New("GitHub 暂时限制访问，请稍后再检查；也可打开版本页面")
	}
	if retry := strings.TrimSpace(response.Header.Get("Retry-After")); retry != "" {
		return fmt.Errorf("GitHub 暂时限制访问，请约 %s 秒后再检查；也可打开版本页面", retry)
	}
	if remaining := strings.TrimSpace(response.Header.Get("X-RateLimit-Remaining")); remaining == "0" {
		if reset := strings.TrimSpace(response.Header.Get("X-RateLimit-Reset")); reset != "" {
			if unix, err := strconv.ParseInt(reset, 10, 64); err == nil {
				when := time.Until(time.Unix(unix, 0))
				if when > 0 {
					return fmt.Errorf("GitHub API 额度已用尽，约 %s 后恢复；也可打开版本页面", formatUpdateWait(when))
				}
			}
		}
		return errors.New("GitHub API 额度已用尽，请稍后再检查；也可打开版本页面")
	}
	return errors.New("GitHub 暂时限制访问，请稍后再检查；也可打开版本页面")
}

func formatUpdateWait(d time.Duration) string {
	minutes := int(d.Round(time.Minute) / time.Minute)
	if minutes < 1 {
		return "不到 1 分钟"
	}
	if minutes < 60 {
		return fmt.Sprintf("约 %d 分钟", minutes)
	}
	return fmt.Sprintf("约 %d 小时", (minutes+59)/60)
}

func (c *UpdateChecker) fetchReleasePage(ctx context.Context, repo string) (UpdateInfo, error) {
	v := updateBase(repo)
	request, err := http.NewRequestWithContext(ctx, "GET", "https://github.com/"+repo+"/releases/latest", nil)
	if err != nil {
		return v, err
	}
	request.Header.Set("Accept", "text/html")
	request.Header.Set("User-Agent", "Duo/"+version)
	if c.pageClient == nil {
		c.pageClient = &http.Client{Timeout: 12 * time.Second}
	}
	response, err := c.pageClient.Do(request)
	if err != nil {
		return v, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return v, fmt.Errorf("GitHub 版本页面返回 HTTP %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
	if err != nil || len(raw) > 8*1024*1024 {
		return v, errors.New("GitHub 版本页面读取失败或内容过大")
	}
	tag := releasePageTag(string(raw), repo, response.Request)
	if tag == "" {
		return v, errors.New("GitHub 版本页面未找到正式版本")
	}
	comparison, err := compareReleaseVersions(tag, version)
	if err != nil {
		return v, err
	}
	releaseURL := "https://github.com/" + repo + "/releases/tag/" + tag
	v.ReleaseURL = releaseLink(releaseURL, repo, "/releases/tag/"+tag)
	if v.ReleaseURL == "" {
		return v, errors.New("GitHub 版本链接与更新仓库不匹配")
	}
	v.Latest = tag
	v.State = "current"
	v.Message = "已通过 GitHub 版本页面确认正式版本（API 暂时受限）。"
	if comparison > 0 {
		v.State = "available"
		v.Message = "发现新版本，可以查看更新说明。"
	} else if comparison < 0 {
		v.State = "ahead"
		v.Message = "本机版本高于仓库最新正式版本。"
	}
	// The page fallback intentionally derives only the two fixed, expected
	// asset names. Downloads still go through the normal host, size and SHA-256
	// validation path before an update is installed.
	v.installer = updateAsset{URL: "https://github.com/" + repo + "/releases/download/" + tag + "/" + installerAssetName, ChecksumURL: "https://github.com/" + repo + "/releases/download/" + tag + "/" + installerAssetName + ".sha256"}
	v.portable = updateAsset{URL: "https://github.com/" + repo + "/releases/download/" + tag + "/" + portableAssetName, ChecksumURL: "https://github.com/" + repo + "/releases/download/" + tag + "/" + portableAssetName + ".sha256"}
	v.InstallerDownloadURL, v.InstallerChecksumURL = v.installer.URL, v.installer.ChecksumURL
	v.PortableDownloadURL, v.PortableChecksumURL = v.portable.URL, v.portable.ChecksumURL
	v.DownloadURL, v.ChecksumURL = v.portable.URL, v.portable.ChecksumURL
	return v, nil
}

func releasePageTag(raw, repo string, response *http.Request) string {
	if response != nil && response.URL != nil {
		prefix := "/" + repo + "/releases/tag/"
		path := response.URL.Path
		if len(path) >= len(prefix) && strings.EqualFold(path[:len(prefix)], prefix) {
			candidate := strings.Trim(path[len(prefix):], "/")
			if stableVersionPattern.MatchString(candidate) {
				return candidate
			}
		}
	}
	pattern := regexp.MustCompile(`(?i)https://github\.com/` + regexp.QuoteMeta(repo) + `/releases/tag/(v[0-9]{1,9}\.[0-9]{1,9}\.[0-9]{1,9})(?:["'<>\s]|$)`)
	match := pattern.FindStringSubmatch(raw)
	if len(match) == 2 && stableVersionPattern.MatchString(match[1]) {
		return match[1]
	}
	return ""
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
			status := http.StatusBadRequest
			if errors.Is(err, errUpdateBusy) {
				status = http.StatusConflict
			}
			fail(w, status, err.Error())
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

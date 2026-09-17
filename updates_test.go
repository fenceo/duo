package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testUpdateChecker(t *testing.T, status int, body string) *UpdateChecker {
	t.Helper()
	c := newUpdateChecker()
	c.client.Transport = updateTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.github.com/repos/owner/jianzuo/releases/latest" || r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Body != nil {
			t.Fatal("Unexpected request", r.URL, r.Method)
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})
	return c
}
func releaseFixture(tag string) string {
	b, _ := json.Marshal(map[string]any{"tag_name": tag, "html_url": "https://github.com/owner/jianzuo/releases/tag/" + tag, "body": "更新说明 <script>not HTML</script>", "assets": []map[string]any{{"name": portableAssetName, "state": "uploaded", "size": 1024, "browser_download_url": "https://github.com/owner/jianzuo/releases/download/" + tag + "/" + portableAssetName, "digest": "sha256:" + strings.Repeat("a", 64)}, {"name": portableAssetName + ".sha256", "state": "uploaded", "size": 90, "browser_download_url": "https://github.com/owner/jianzuo/releases/download/" + tag + "/" + portableAssetName + ".sha256"}}})
	return string(b)
}
func TestReleaseVersionComparison(t *testing.T) {
	for _, tc := range []struct {
		r, l string
		want int
	}{{"v0.12.0", "0.9.9-portable", 1}, {"v0.12.0", "0.12.0-portable", 0}, {"v0.9.9", "0.12.0-portable", -1}, {"v1.0.0", "0.99.99-portable", 1}, {"v0.12.10", "0.12.9-portable", 1}} {
		got, e := compareReleaseVersions(tc.r, tc.l)
		if e != nil || got != tc.want {
			t.Fatal(tc, got, e)
		}
	}
	for _, bad := range []string{"latest", "v1.0.0-beta", "../1.0", "-1.0.0", "1.2.99999999999999"} {
		if _, e := compareReleaseVersions(bad, version); e == nil {
			t.Fatal(bad)
		}
	}
}
func TestUpdateRepositoryAndLinks(t *testing.T) {
	canonical := "https://github.com/Owner/Jianzuo/releases/tag/v1.0.0"
	if releaseLink(canonical, "owner/jianzuo", "/releases/tag/v1.0.0") != canonical {
		t.Fatal("GitHub repository names are case insensitive")
	}
	for _, in := range []string{"owner/jianzuo", "https://github.com/owner/jianzuo/", "https://github.com/owner/jianzuo.git"} {
		got, e := normalizeRepository(in)
		if e != nil || got != "owner/jianzuo" {
			t.Fatal(got, e)
		}
	}
	for _, in := range []string{"http://localhost/repo", "owner/../secret", "https://github.com/owner/repo?token=x", "https://github.com.evil.test/owner/repo", "owner/repo#x", "https://user:pass@github.com/owner/repo", "owner/..", "owner/%2e%2e"} {
		if _, e := normalizeRepository(in); e == nil {
			t.Fatal(in)
		}
	}
	for _, u := range []string{"http://github.com/owner/jianzuo/releases/tag/v1.0.0", "https://github.com.evil.test/owner/jianzuo/releases/tag/v1.0.0", "https://github.com/other/jianzuo/releases/tag/v1.0.0", "https://user@github.com/owner/jianzuo/releases/tag/v1.0.0", "https://github.com/owner/jianzuo/releases/tag/v1.0.0?x=1"} {
		if releaseLink(u, "owner/jianzuo", "/releases/tag/v1.0.0") != "" {
			t.Fatal(u)
		}
	}
}
func TestUpdateReleaseResultsAndCache(t *testing.T) {
	c := testUpdateChecker(t, 200, releaseFixture("v99.0.0"))
	v, e := c.check(context.Background(), "owner/jianzuo")
	if e != nil || v.State != "available" || v.DownloadURL == "" || v.ChecksumURL == "" || v.Digest == "" || v.Checked == 0 {
		t.Fatal(v, e)
	}
	c.client.Transport = updateTransport(func(*http.Request) (*http.Response, error) { t.Fatal("cache fetched network"); return nil, nil })
	again, e := c.check(context.Background(), "owner/jianzuo")
	if e != nil || again.Checked != v.Checked {
		t.Fatal(again, e)
	}
	for _, tc := range []struct{ body, state string }{{releaseFixture("v" + strings.TrimSuffix(version, "-portable")), "current"}, {releaseFixture("v0.0.1"), "ahead"}, {`{"draft":true}`, "no_release"}, {`{"prerelease":true}`, "no_release"}} {
		v, e = testUpdateChecker(t, 200, tc.body).fetch(context.Background(), "owner/jianzuo")
		if e != nil || v.State != tc.state {
			t.Fatal(v, e)
		}
	}
	v, e = testUpdateChecker(t, 404, "").fetch(context.Background(), "owner/jianzuo")
	if e != nil || v.State != "no_release" {
		t.Fatal(v, e)
	}
	for _, code := range []int{403, 429, 500} {
		if _, e = testUpdateChecker(t, code, "").fetch(context.Background(), "owner/jianzuo"); e == nil {
			t.Fatal(code)
		}
	}
	body := strings.ReplaceAll(releaseFixture("v99.0.0"), "https://github.com/owner/jianzuo/releases/download/", "https://evil.test/file/")
	v, e = testUpdateChecker(t, 200, body).fetch(context.Background(), "owner/jianzuo")
	if e != nil || v.State != "available" || v.DownloadURL != "" {
		t.Fatal(v, e)
	}
	if _, e = testUpdateChecker(t, 200, "not json").fetch(context.Background(), "owner/jianzuo"); e == nil {
		t.Fatal("invalid JSON accepted")
	}
	if _, e = testUpdateChecker(t, 200, strings.Repeat(" ", 2*1024*1024+1)).fetch(context.Background(), "owner/jianzuo"); e == nil {
		t.Fatal("oversize accepted")
	}
}
func TestUpdateFailureAndUnconfigured(t *testing.T) {
	c := newUpdateChecker()
	c.client.Transport = updateTransport(func(r *http.Request) (*http.Response, error) { return nil, errors.New("private-network-detail") })
	v, e := c.check(context.Background(), "")
	if e != nil || v.State != "unconfigured" {
		t.Fatal(v, e)
	}
	_, e = c.check(context.Background(), "owner/jianzuo")
	if e == nil || strings.Contains(e.Error(), "private-network-detail") {
		t.Fatal(e)
	}
	if c.snapshot("owner/jianzuo").State != "unchecked" {
		t.Fatal("failure presented as success")
	}
	if time.Since(time.UnixMilli(c.cached.Checked)) > time.Second {
		t.Fatal("check timestamp")
	}
}

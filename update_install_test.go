package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testPEFile() []byte {
	file := make([]byte, 128)
	copy(file, "MZ")
	binary.LittleEndian.PutUint32(file[60:64], 64)
	copy(file[64:68], []byte{'P', 'E', 0, 0})
	return file
}

type testZipEntry struct {
	name string
	data []byte
}

func testPortableZip(t *testing.T, entries []testZipEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		file, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = file.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func validPortableZip(t *testing.T) []byte {
	t.Helper()
	pe := testPEFile()
	return testPortableZip(t, []testZipEntry{
		{name: "简作.exe", data: pe},
		{name: "jianzuo-service.exe", data: pe},
		{name: "使用说明.md", data: []byte("portable")},
		{name: "THIRD-PARTY-NOTICES.txt", data: []byte("notices")},
	})
}

func archiveResponse(request *http.Request, status int, body []byte, contentLength int64) (*http.Response, error) {
	return &http.Response{
		StatusCode:    status,
		Body:          io.NopCloser(bytes.NewReader(body)),
		Header:        http.Header{},
		ContentLength: contentLength,
		Request:       request,
	}, nil
}

func TestDownloadPortableArchiveChecksHashAndSize(t *testing.T) {
	archive := validPortableZip(t)
	sum := sha256.Sum256(archive)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	archiveURL := "https://github.com/owner/jianzuo/releases/download/v0.16.0/" + portableAssetName
	checksumURL := archiveURL + ".sha256"

	t.Run("release digest", func(t *testing.T) {
		checker := newUpdateChecker()
		checker.downloadClient = &http.Client{Transport: updateTransport(func(request *http.Request) (*http.Response, error) {
			if request.URL.String() != archiveURL {
				t.Fatal("unexpected URL", request.URL)
			}
			return archiveResponse(request, http.StatusOK, archive, int64(len(archive)))
		})}
		path, err := downloadPortableArchive(context.Background(), checker, UpdateInfo{
			DownloadURL: archiveURL,
			Size:        int64(len(archive)),
			Digest:      digest,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)
		raw, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(raw, archive) {
			t.Fatal("downloaded archive differs")
		}
	})

	t.Run("checksum asset", func(t *testing.T) {
		checker := newUpdateChecker()
		checker.downloadClient = &http.Client{Transport: updateTransport(func(request *http.Request) (*http.Response, error) {
			switch request.URL.String() {
			case checksumURL:
				return archiveResponse(request, http.StatusOK, []byte(hex.EncodeToString(sum[:])+"  "+portableAssetName+"\n"), -1)
			case archiveURL:
				return archiveResponse(request, http.StatusOK, archive, int64(len(archive)))
			default:
				t.Fatal("unexpected URL", request.URL)
				return nil, nil
			}
		})}
		path, err := downloadPortableArchive(context.Background(), checker, UpdateInfo{
			DownloadURL: archiveURL,
			ChecksumURL: checksumURL,
			Size:        int64(len(archive)),
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(path)
	})

	t.Run("size mismatch", func(t *testing.T) {
		checker := newUpdateChecker()
		checker.downloadClient = &http.Client{Transport: updateTransport(func(request *http.Request) (*http.Response, error) {
			return archiveResponse(request, http.StatusOK, archive, int64(len(archive)))
		})}
		_, err := downloadPortableArchive(context.Background(), checker, UpdateInfo{
			DownloadURL: archiveURL,
			Size:        int64(len(archive) + 1),
			Digest:      digest,
		})
		if err == nil || !strings.Contains(err.Error(), "大小") {
			t.Fatal(err)
		}
	})

	t.Run("hash mismatch", func(t *testing.T) {
		checker := newUpdateChecker()
		checker.downloadClient = &http.Client{Transport: updateTransport(func(request *http.Request) (*http.Response, error) {
			return archiveResponse(request, http.StatusOK, archive, int64(len(archive)))
		})}
		_, err := downloadPortableArchive(context.Background(), checker, UpdateInfo{
			DownloadURL: archiveURL,
			Size:        int64(len(archive)),
			Digest:      "sha256:" + strings.Repeat("0", 64),
		})
		if err == nil || !strings.Contains(err.Error(), "SHA-256") {
			t.Fatal(err)
		}
	})

	t.Run("declared archive too large", func(t *testing.T) {
		_, err := downloadPortableArchive(context.Background(), newUpdateChecker(), UpdateInfo{Size: maxPortableArchiveSize + 1})
		if err == nil || !strings.Contains(err.Error(), "超过") {
			t.Fatal(err)
		}
	})

	t.Run("response too large", func(t *testing.T) {
		checker := newUpdateChecker()
		checker.downloadClient = &http.Client{Transport: updateTransport(func(request *http.Request) (*http.Response, error) {
			return archiveResponse(request, http.StatusOK, archive, maxPortableArchiveSize+1)
		})}
		_, err := downloadPortableArchive(context.Background(), checker, UpdateInfo{DownloadURL: archiveURL, Digest: digest})
		if err == nil || !strings.Contains(err.Error(), "超过") {
			t.Fatal(err)
		}
	})
}

func TestExtractPortableArchiveValidation(t *testing.T) {
	writeArchive := func(t *testing.T, raw []byte) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "update.zip")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	valid := validPortableZip(t)
	validPath := writeArchive(t, valid)
	stage := t.TempDir()
	if err := extractPortableArchive(validPath, stage); err != nil {
		t.Fatal(err)
	}
	if err := validatePortableStaging(stage); err != nil {
		t.Fatal(err)
	}

	for name, raw := range map[string][]byte{
		"path traversal": testPortableZip(t, []testZipEntry{{name: "../evil.exe", data: []byte("x")}}),
		"duplicate": testPortableZip(t, []testZipEntry{
			{name: "简作.exe", data: testPEFile()},
			{name: "简作.exe", data: testPEFile()},
		}),
		"missing": testPortableZip(t, []testZipEntry{{name: "简作.exe", data: testPEFile()}}),
	} {
		t.Run(name, func(t *testing.T) {
			if err := extractPortableArchive(writeArchive(t, raw), t.TempDir()); err == nil {
				t.Fatal("invalid archive accepted")
			}
		})
	}

	badPE := testPortableZip(t, []testZipEntry{
		{name: "简作.exe", data: testPEFile()},
		{name: "jianzuo-service.exe", data: []byte("not a PE")},
		{name: "使用说明.md", data: []byte("portable")},
		{name: "THIRD-PARTY-NOTICES.txt", data: []byte("notices")},
	})
	badStage := t.TempDir()
	if err := extractPortableArchive(writeArchive(t, badPE), badStage); err != nil {
		t.Fatal(err)
	}
	if err := validatePortableStaging(badStage); err == nil {
		t.Fatal("invalid executable accepted")
	}
	if _, err := os.Stat(filepath.Join(stage, "..", "evil.exe")); err == nil {
		t.Fatal("path traversal output escaped stage")
	}
}

func TestPortableFileReplacementAndRollback(t *testing.T) {
	root, stage := t.TempDir(), t.TempDir()
	oldContents := map[string]string{}
	for _, name := range portableArchiveFiles {
		oldContents[name] = "old-" + name
		if err := os.WriteFile(filepath.Join(root, name), []byte(oldContents[name]), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(stage, name), []byte("new-"+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	backup, err := replacePortableFiles(root, stage)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range portableArchiveFiles {
		raw, readErr := os.ReadFile(filepath.Join(root, name))
		if readErr != nil || string(raw) != "new-"+name {
			t.Fatal(name, string(raw), readErr)
		}
	}
	if err = rollbackPortableFiles(root, backup); err != nil {
		t.Fatal(err)
	}
	for _, name := range portableArchiveFiles {
		raw, readErr := os.ReadFile(filepath.Join(root, name))
		if readErr != nil || string(raw) != oldContents[name] {
			t.Fatal(name, string(raw), readErr)
		}
	}

	failingStage := t.TempDir()
	for _, name := range portableArchiveFiles[:len(portableArchiveFiles)-1] {
		if err = os.WriteFile(filepath.Join(failingStage, name), []byte("new-"+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = replacePortableFiles(root, failingStage); err == nil {
		t.Fatal("incomplete stage accepted")
	}
	for _, name := range portableArchiveFiles {
		raw, readErr := os.ReadFile(filepath.Join(root, name))
		if readErr != nil || string(raw) != oldContents[name] {
			t.Fatal("failed replacement was not rolled back", name, string(raw), readErr)
		}
	}
}

func TestPortableHealthURL(t *testing.T) {
	tests := map[string]string{
		`{"listen":"0.0.0.0:8789"}`:  "http://127.0.0.1:8789/healthz",
		`{"listen":":18789"}`:        "http://127.0.0.1:18789/healthz",
		`{"listen":"[::]:8789"}`:     "http://[::1]:8789/healthz",
		`{"listen":"127.0.0.1:9000"}`: "http://127.0.0.1:9000/healthz",
	}
	for raw, expected := range tests {
		t.Run(raw, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			actual, err := portableHealthURL(dir)
			if err != nil {
				t.Fatal(err)
			}
			if actual != expected {
				t.Fatalf("got %q, want %q", actual, expected)
			}
		})
	}
}

func TestWaitForPortableHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"app":"jianzuo","version":"0.16.0-portable"}`)
	}))
	defer server.Close()
	if err := waitForPortableHealth(server.Client(), server.URL, "0.16.0-portable", time.Second); err != nil {
		t.Fatal(err)
	}

	mismatch := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"app":"jianzuo","version":"0.15.0-portable"}`)
	}))
	defer mismatch.Close()
	err := waitForPortableHealth(mismatch.Client(), mismatch.URL, "0.16.0-portable", 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "健康检查") {
		t.Fatal(err)
	}
}

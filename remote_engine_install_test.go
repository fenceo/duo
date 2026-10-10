package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Execute the installer with isolated, synthetic Node/npm/download fixtures.
// No network, real npm package, account or model is used by these regressions.
func TestRemoteEngineInstallerRuntimeAndFailures(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil && runtime.GOOS == "windows" {
		git, gitErr := exec.LookPath("git")
		if gitErr == nil {
			candidate := filepath.Join(filepath.Dir(filepath.Dir(git)), "usr", "bin", "sh.exe")
			if _, statErr := os.Stat(candidate); statErr == nil {
				shell, err = candidate, nil
			}
		}
	}
	if err != nil {
		t.Skip("POSIX shell required for isolated remote installer regression")
	}
	for _, tc := range []struct {
		name, native, failure, arch, wantCode string
	}{
		{name: "no node or npm", arch: "x86_64"},
		{name: "Windows npm shim", native: "windows", arch: "x86_64"},
		{name: "old native node", native: "old", arch: "x86_64"},
		{name: "usable native runtime", native: "native", arch: "x86_64"},
		{name: "arm64 runtime", arch: "aarch64"},
		{name: "checksum mismatch", failure: "checksum", arch: "x86_64", wantCode: "runtime_checksum"},
		{name: "download failed", failure: "download", arch: "x86_64", wantCode: "runtime_download"},
		{name: "unsupported architecture", arch: "riscv64", wantCode: "runtime_arch"},
		{name: "npm permission", failure: "permission", arch: "x86_64", wantCode: "package_permission"},
		{name: "npm network", failure: "network", arch: "x86_64", wantCode: "package_network"},
		{name: "npm space", failure: "space", arch: "x86_64", wantCode: "package_space"},
		{name: "npm other", failure: "other", arch: "x86_64", wantCode: "package_install"},
		{name: "missing entry", failure: "missing", arch: "x86_64", wantCode: "entry_missing"},
		{name: "entry startup", failure: "startup", arch: "x86_64", wantCode: "entry_start"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// Ask the shell for its own path representation (MSYS on Windows).
			pwd := exec.Command(shell, "-c", "pwd -P")
			pwd.Dir = dir
			out, err := pwd.Output()
			if err != nil {
				t.Fatal(err)
			}
			posixDir := strings.TrimSpace(string(out))
			bin := filepath.Join(dir, "fixture-bin")
			if err = os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			write := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0700); err != nil {
					t.Fatal(err)
				}
			}
			write("uname", "#!/bin/sh\ncase \"$1\" in -s) echo Linux;; -m) echo \"$DUO_FIXTURE_ARCH\";; esac\n")
			write("curl", "#!/bin/sh\nprintf called >> \"$DUO_FIXTURE_DIR/downloads\"\n[ \"$DUO_FIXTURE_FAILURE\" != download ] || exit 1\nwhile [ \"$#\" -gt 0 ]; do if [ \"$1\" = -o ]; then cp \"$DUO_FIXTURE_DIR/runtime.tar.gz\" \"$2\"; exit; fi; shift; done\nexit 1\n")
			if tc.native != "" {
				write("node", remoteInstallFixtureNode)
				write("npm", "synthetic npm entry\n")
			}
			arch := "x64"
			if tc.arch == "aarch64" {
				arch = "arm64"
			}
			var archive bytes.Buffer
			gz := gzip.NewWriter(&archive)
			tarWriter := tar.NewWriter(gz)
			for _, entry := range []struct{ name, body string }{
				{"bin/node", remoteInstallFixtureNode},
				{"lib/node_modules/npm/bin/npm-cli.js", "synthetic npm entry\n"},
			} {
				if err = tarWriter.WriteHeader(&tar.Header{Name: "node-v" + managedNodeVersion + "-linux-" + arch + "/" + entry.name, Mode: 0700, Size: int64(len(entry.body))}); err != nil {
					t.Fatal(err)
				}
				if _, err = tarWriter.Write([]byte(entry.body)); err != nil {
					t.Fatal(err)
				}
			}
			if err = tarWriter.Close(); err != nil {
				t.Fatal(err)
			}
			if err = gz.Close(); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, "runtime.tar.gz"), archive.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			script := remoteEngineInstallScript("@deepseek-ai/dsh", "dsh", "fixture")
			checksum := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
			if tc.failure != "checksum" {
				script = strings.NewReplacer(managedNodeLinuxX64SHA256, checksum, managedNodeLinuxARM64SHA256, checksum).Replace(script)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, shell, "-s")
			cmd.Dir = dir
			cmd.Stdin = strings.NewReader(script)
			cmd.Env = append(os.Environ(), "HOME="+posixDir+"/home with spaces", "PATH="+filepath.ToSlash(bin)+string(os.PathListSeparator)+filepath.ToSlash(filepath.Dir(shell))+string(os.PathListSeparator)+os.Getenv("PATH"),
				"DUO_FIXTURE_DIR="+posixDir, "DUO_FIXTURE_ARCH="+tc.arch, "DUO_FIXTURE_NATIVE="+tc.native, "DUO_FIXTURE_FAILURE="+tc.failure)
			// npm fixtures never write outside this synthetic home.
			out, err = cmd.CombinedOutput()
			if strings.Contains(string(out), "synthetic-secret") {
				t.Fatal("private package diagnostic leaked")
			}
			if tc.wantCode != "" {
				if err == nil || !strings.Contains(string(out), "__DUO_ENGINE_ERROR__\n"+tc.wantCode+"\n") {
					t.Fatalf("expected %s: %v (%s)", tc.wantCode, err, out)
				}
			} else if err != nil || !strings.Contains(string(out), "__DUO_ENGINE_PATH__\n"+posixDir+"/home with spaces/.local/share/duo/engine-tools/fixture/duo-bin/dsh") {
				t.Fatalf("installer failed: %v (%s)", err, out)
			}
			prefix := filepath.Join(dir, "home with spaces", ".local", "share", "duo", "engine-tools", "fixture")
			if tc.wantCode == "" {
				verify := exec.CommandContext(ctx, shell, filepath.Join(prefix, "duo-bin", "dsh"), "--version")
				verify.Env = cmd.Env // No installation-only runtime PATH.
				result, err := verify.CombinedOutput()
				if err != nil || !strings.Contains(string(result), "synthetic CLI version") {
					t.Fatalf("saved launcher cannot start independently: %v (%s)", err, result)
				}
			}
			privateLogs, _ := filepath.Glob(filepath.Join(prefix, "npm-output.*"))
			if len(privateLogs) != 0 {
				t.Fatal("private installer output was retained")
			}
			if _, err = os.Stat(filepath.Join(prefix, "node.tar.gz")); !os.IsNotExist(err) {
				t.Fatal("download archive was retained")
			}
			if tc.native == "native" {
				if _, err := os.Stat(filepath.Join(dir, "downloads")); !os.IsNotExist(err) {
					t.Fatal("usable native runtime unnecessarily downloaded")
				}
			}
		})
	}
}

const remoteInstallFixtureNode = `#!/bin/sh
set -eu
if [ "$1" = -p ]; then
  if [ "$2" = process.execPath ]; then printf '%s/node\n' "$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)";
  else case "$0" in */fixture-bin/node) if [ "$DUO_FIXTURE_NATIVE" = native ]; then echo true; else echo false; fi;; *) echo true;; esac; fi
  exit
fi
if [ "$2" = --version ]; then
  case "$1" in */bin/dsh) [ "$DUO_FIXTURE_FAILURE" != startup ] || exit 1;; esac
  echo 'synthetic CLI version'; exit
fi
shift
prefix=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = --prefix ]; then prefix="$2"; fi
  shift
done
case "$DUO_FIXTURE_FAILURE" in
  permission) echo 'npm error code EACCES'; echo 'https://synthetic-secret@proxy.example'; exit 1;;
  network) echo 'npm ERR! code ENOTFOUND'; echo 'synthetic-secret'; exit 1;;
  space) echo 'npm error code ENOSPC'; exit 1;;
  other) echo 'private synthetic-secret'; exit 1;;
  missing) exit 0;;
esac
mkdir -p "$prefix/bin"
printf '#!/usr/bin/env node\n' > "$prefix/bin/dsh"
chmod 700 "$prefix/bin/dsh"
`

func TestRemoteInstallDiagnosticDoesNotExposePrivateOutput(t *testing.T) {
	for _, code := range []string{"runtime_download", "runtime_checksum", "package_network", "package_permission", "entry_start", "unknown"} {
		err := remoteEngineInstallError("private synthetic-secret\n__DUO_ENGINE_ERROR__\n" + code + "\nproxy=https://synthetic-secret@example.invalid\n")
		if err == nil || strings.Contains(err.Error(), "synthetic-secret") || strings.Contains(err.Error(), "example.invalid") {
			t.Fatal("private diagnostic exposed", err)
		}
	}
}

func TestRemoteEngineInstallWSLIntegration(t *testing.T) {
	distro, user := os.Getenv("DUO_TEST_REMOTE_INSTALL_DISTRO"), os.Getenv("DUO_TEST_REMOTE_INSTALL_USER")
	if distro == "" || user == "" {
		t.Skip("explicit user-selected WSL install and version check only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	path, err := installRemoteEngine(ctx, Environment{Type: "wsl", Distro: distro, User: user}, "deepseek-harness", fmt.Sprintf("verified-harness-%d", time.Now().UnixNano()), func(message string) { t.Log(message) })
	if err != nil {
		t.Fatal(err)
	}
	verify := commandWithContext(ctx, environmentProbeCommand(Environment{Type: "wsl", Distro: distro, User: user}, path, "--version"))
	out, err := verify.Output()
	if err != nil {
		t.Fatal("saved entry failed outside the installation shell", err)
	}
	t.Log("installed entry", path, "version", strings.TrimSpace(string(out)))
}

package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Desktop updates replace the hash-named CLI directory. Repair only a missing
// path inside this user's managed installation; custom paths remain explicit.
// This resolves execution without rewriting task/environment snapshots.
func resolveManagedCodexPath(configured string) string {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" || !filepath.IsAbs(configured) {
		return configured
	}
	if _, err := os.Stat(configured); !os.IsNotExist(err) {
		return configured
	}
	root := filepath.Join(local, "OpenAI", "Codex", "bin")
	dir := filepath.Dir(configured)
	validHash := func(value string) bool {
		_, err := hex.DecodeString(value)
		return len(value) == 16 && err == nil
	}
	if !strings.EqualFold(filepath.Base(configured), "codex.exe") || !strings.EqualFold(filepath.Dir(dir), root) || !validHash(filepath.Base(dir)) {
		return configured
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return configured
	}
	best := configured
	var newest time.Time
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !validHash(entry.Name()) {
			continue
		}
		candidate := filepath.Join(root, entry.Name(), "codex.exe")
		info, err := os.Lstat(candidate)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if best == configured || info.ModTime().After(newest) {
			best, newest = candidate, info.ModTime()
		}
	}
	return best
}

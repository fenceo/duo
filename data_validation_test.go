package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func dataValidationFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Duo 数据 # %")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	c := Config{Listen: "127.0.0.1:8789", Codex: "codex", Workspaces: []string{t.TempDir()}}
	raw, _ := json.Marshal(map[string]any{"password": "synthetic-password", "config": c})
	if err := initializePortable(dir, bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	return dir
}

func dataValidationSnapshot(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatal("unexpected fixture directory")
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[entry.Name()] = sha256.Sum256(raw)
	}
	return out
}

func TestValidateExistingDataReadOnly(t *testing.T) {
	dir := dataValidationFixture(t)
	before := dataValidationSnapshot(t, dir)
	if err := validateExistingData(dir); err != nil {
		t.Fatal(err)
	}
	after := dataValidationSnapshot(t, dir)
	if len(before) != len(after) {
		t.Fatal("validation created files")
	}
	for name, hash := range before {
		if after[name] != hash {
			t.Fatal("validation altered", name)
		}
	}
	missing := filepath.Join(t.TempDir(), "not-created")
	if validateExistingData(missing) == nil {
		t.Fatal("missing data accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("validation created directory")
	}
}

func TestValidateExistingDataRejectsUnsafeInputs(t *testing.T) {
	for _, kind := range []string{"config", "database", "wal", "journal", "password", "running", "schema"} {
		t.Run(kind, func(t *testing.T) {
			dir := dataValidationFixture(t)
			var err error
			switch kind {
			case "config":
				err = os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"listen":"bad"}`), 0600)
			case "database":
				err = os.WriteFile(filepath.Join(dir, "jianzuo.db"), []byte("not sqlite"), 0600)
			case "wal", "journal":
				err = os.WriteFile(filepath.Join(dir, "jianzuo.db-"+kind), []byte("uncheckpointed"), 0600)
			default:
				var db *sql.DB
				db, err = sql.Open("sqlite", filepath.Join(dir, "jianzuo.db"))
				if err != nil {
					t.Fatal(err)
				}
				query := "UPDATE settings SET value='bad' WHERE key='password_hash'"
				if kind == "running" {
					query = "INSERT INTO tasks VALUES('test','test','test','','','running',0,0)"
				}
				if kind == "schema" {
					query = "DROP TABLE events"
				}
				_, err = db.Exec(query)
				db.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			before := dataValidationSnapshot(t, dir)
			if validateExistingData(dir) == nil {
				t.Fatal("invalid data accepted:", kind)
			}
			after := dataValidationSnapshot(t, dir)
			if len(before) != len(after) {
				t.Fatal("rejected validation created files")
			}
			for name, hash := range before {
				if after[name] != hash {
					t.Fatal("rejected validation altered", name)
				}
			}
		})
	}
}

func TestValidateExistingDataRejectsReadonlyFiles(t *testing.T) {
	for _, name := range []string{"config.json", "jianzuo.db"} {
		t.Run(name, func(t *testing.T) {
			dir := dataValidationFixture(t)
			path := filepath.Join(dir, name)
			before := dataValidationSnapshot(t, dir)
			if err := os.Chmod(path, 0400); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(path, 0600) })
			if validateExistingData(dir) == nil {
				t.Fatal("readonly data accepted")
			}
			after := dataValidationSnapshot(t, dir)
			for file, hash := range before {
				if after[file] != hash {
					t.Fatal("readonly validation altered", file)
				}
			}
		})
	}
}

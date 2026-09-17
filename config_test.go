package main

import (
	"os"
	"testing"
)

func TestConfigSaveReloadsAndRemovesTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	c, err := loadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	v := c.get()
	if len(v.Environments) == 0 {
		t.Fatal("expected a normalized default environment")
	}
	v.Environments[0].Model = "saved-model"
	if err = c.save(v); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(c.path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary config still exists: %v", err)
	}
	loaded, err := loadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.get().Model; got != "saved-model" {
		t.Fatalf("model = %q, want saved-model", got)
	}
	if got := loaded.get().Environments[0].Model; got != "saved-model" {
		t.Fatalf("environment model = %q, want saved-model", got)
	}
}

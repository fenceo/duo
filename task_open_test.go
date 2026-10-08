package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTaskOpenURLReadsRunningServiceWithoutReset(t *testing.T) {
	a, task := switchFixture(t, &switchRunner{})
	a.store.Exec("UPDATE tasks SET status='running' WHERE id=?", task.ID)
	for _, address := range []string{"0.0.0.0:8789", "[::]:8789", "127.0.0.1:8789"} {
		link, err := taskOpenURL(a.store.directory, address, task.ID)
		if err != nil || link != "http://127.0.0.1:8789/?task="+task.ID {
			t.Fatal("incorrect reopen link", link, err)
		}
	}
	if link, err := taskOpenURL(a.store.directory, "", task.ID); err != nil || link == "" {
		t.Fatal("existing config not read", link, err)
	}
	saved, _ := a.store.task(task.ID)
	if saved.Session != task.Session || saved.Status != "running" {
		t.Fatal("opening task modified execution state", saved)
	}
	for _, address := range []string{"127.0.0.1:0", "http://bad/path", "untrusted.example:8789", "localhost:70000"} {
		if _, err := taskOpenURL(a.store.directory, address, task.ID); err == nil {
			t.Fatal("invalid address accepted", address)
		}
	}
	a.store.Exec("INSERT INTO task_options(task_id,deleted) VALUES(?,1) ON CONFLICT(task_id) DO UPDATE SET deleted=1", task.ID)
	if _, err := taskOpenURL(a.store.directory, "127.0.0.1:8789", task.ID); err == nil {
		t.Fatal("deleted task reopened")
	}
}

func TestTaskOpenURLNeverInitializesMissingData(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "not-created")
	if _, err := taskOpenURL(directory, "127.0.0.1:8789", "known-task"); err == nil {
		t.Fatal("missing database accepted")
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatal("open-task initialized data", err)
	}
}

func TestTaskOpenCommandQuotesPathsAndUsesActualPort(t *testing.T) {
	command := taskOpenCommand("C:/Duo App/service.exe", "C:/user's data/$var", "task-123", "127.0.0.1:54321")
	if runtime.GOOS == "windows" {
		if command != "& 'C:/Duo App/service.exe' --data 'C:/user''s data/$var' --open-task 'task-123' --listen '127.0.0.1:54321'" {
			t.Fatal("PowerShell command was not quoted literally", command)
		}
	} else if !strings.Contains(command, "'C:/user'\"'\"'s data/$var'") {
		t.Fatal("POSIX command was not quoted literally", command)
	}
	a, task := switchFixture(t, &switchRunner{})
	a.hardwareAddress = "127.0.0.1:54321"
	request := toolsClient(t, a)
	var response struct{ Command string }
	if err := json.Unmarshal(request("/api/tasks/"+task.ID+"/open-command", "GET", nil, 200), &response); err != nil || !strings.Contains(response.Command, "'127.0.0.1:54321'") || !strings.Contains(response.Command, task.ID) || !strings.Contains(response.Command, a.store.directory) {
		t.Fatal("copied command used wrong task, data or port", response.Command, err)
	}
}

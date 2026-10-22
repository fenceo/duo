package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAttachmentStageCancellationAndDeadline(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"cancelled", context.Canceled, "环境命令已取消"},
		{"deadline", context.DeadlineExceeded, "环境命令超时"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// An already-finished context prevents any WSL process from starting.
			var ctx context.Context
			var cancel context.CancelFunc
			if tc.err == context.Canceled {
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			} else {
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			defer cancel()
			task := Task{Environment: &Environment{Type: "wsl", Distro: "synthetic-never-started"}}
			files := []RuntimeAttachment{{Attachment: Attachment{ID: "fixture", Name: "history.md"}, Data: []byte("synthetic history")}}
			var app App
			staged, cleanup, err := app.stageRuntimeFiles(ctx, task, files)
			if staged != nil || cleanup != nil || !errors.Is(err, tc.err) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("stage failure = %v, want %v and %q", err, tc.err, tc.want)
			}
			if tc.err == context.Canceled && strings.Contains(err.Error(), "超时") {
				t.Fatalf("cancellation was misreported as timeout: %v", err)
			}
		})
	}
}

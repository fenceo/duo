package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type harnessTrackedPipe struct {
	*io.PipeWriter
	started, finished chan struct{}
}

func (p harnessTrackedPipe) Write(data []byte) (int, error) {
	close(p.started)
	defer close(p.finished)
	return p.PipeWriter.Write(data)
}

type harnessDiscardWriter struct{}

func (harnessDiscardWriter) Write(data []byte) (int, error) { return len(data), nil }
func (harnessDiscardWriter) Close() error                   { return nil }

type harnessFailedReader struct{ err error }

func (r harnessFailedReader) Read([]byte) (int, error) { return 0, r.err }

func harnessIOWorker() *harnessWorker {
	return &harnessWorker{
		frames: make(chan codexRPC, 8), fault: make(chan error, 1),
		done: make(chan struct{}), halt: make(chan struct{}),
	}
}

func TestHarnessSendCancellationJoinsBlockedWriter(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	tracked := harnessTrackedPipe{PipeWriter: writer, started: make(chan struct{}), finished: make(chan struct{})}
	w := harnessIOWorker()
	w.in = tracked
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		_, err := w.send(ctx, "session/prompt", map[string]any{"text": strings.Repeat("x", 200000)})
		returned <- err
	}()
	select {
	case <-tracked.started:
	case <-time.After(2 * time.Second):
		t.Fatal("send never reached the blocked pipe")
	}
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled send returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not unblock stdin writing")
	}
	select {
	case <-tracked.finished:
	default:
		t.Fatal("cancelled send leaked its writer goroutine")
	}
}

func TestHarnessBootstrapWriteRespectsDeadline(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	tracked := harnessTrackedPipe{PipeWriter: writer, started: make(chan struct{}), finished: make(chan struct{})}
	w := harnessIOWorker()
	w.in = tracked
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := w.write(ctx, []byte("{\"cwd\":\"/work\"}\n")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bootstrap write ignored its startup deadline: %v", err)
	}
	select {
	case <-tracked.finished:
	default:
		t.Fatal("timed-out bootstrap writer remains running")
	}
}

func TestHarnessStdoutOversizeClosesFramesAfterQueuedMessages(t *testing.T) {
	w := harnessIOWorker()
	w.in = harnessDiscardWriter{}
	// A complete response must remain available even though the next record
	// exceeds the frame bound and neither stderr nor the child has exited.
	w.scan(strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"accepted\":true}}\n"+strings.Repeat("x", 4*1024*1024+1)), true)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := w.request(ctx, "initialize", nil)
	if err != nil || string(result) != `{"accepted":true}` {
		t.Fatalf("complete queued response lost on scanner exit: %s, %v", result, err)
	}
	if _, err := w.request(ctx, "session/prompt", nil); err == nil || !strings.Contains(err.Error(), "stdout") || !strings.Contains(err.Error(), "token too long") {
		t.Fatalf("oversized stdout did not promptly fail the next read: %v", err)
	}
	select {
	case <-w.done:
		t.Fatal("test unexpectedly relied on process exit")
	default:
	}
}

func TestHarnessStdoutEOFDrainsAllCompleteFrames(t *testing.T) {
	w := harnessIOWorker()
	w.scan(strings.NewReader("{\"id\":1,\"result\":{}}\n{\"id\":2,\"result\":{}}\n"), true)
	var ids []string
	for frame := range w.frames {
		ids = append(ids, string(frame.ID))
	}
	if strings.Join(ids, ",") != "1,2" {
		t.Fatalf("EOF discarded queued frames: %v", ids)
	}
}

func TestHarnessStderrFailureInterruptsRequest(t *testing.T) {
	w := harnessIOWorker()
	w.in = harnessDiscardWriter{}
	injected := errors.New("injected stderr failure")
	w.scan(harnessFailedReader{err: injected}, false)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := w.request(ctx, "initialize", nil); !errors.Is(err, injected) || !strings.Contains(err.Error(), "stderr") {
		t.Fatalf("stderr failure did not interrupt request: %v", err)
	}
	select {
	case _, ok := <-w.frames:
		if !ok {
			t.Fatal("stderr closed the stdout-owned frame channel")
		}
	default:
	}
}

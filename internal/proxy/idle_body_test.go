package proxy

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

type ctxBlockingReader struct {
	ctx context.Context
}

func (r *ctxBlockingReader) Read(p []byte) (int, error) {
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func (r *ctxBlockingReader) Close() error { return nil }

func TestIdleTimeoutBody_StalledBackendIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	body := newIdleTimeoutBody(&ctxBlockingReader{ctx: ctx}, 100*time.Millisecond, cancel)

	start := time.Now()
	_, err := body.Read(make([]byte, 10))
	if err == nil {
		t.Fatal("read from a stalled backend did not fail")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("stalled backend was cancelled after %v, want about 100ms", elapsed)
	}
}

func TestIdleTimeoutBody_SlowConsumerIsNotCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	body := newIdleTimeoutBody(io.NopCloser(strings.NewReader(strings.Repeat("z", 50))), 100*time.Millisecond, cancel)

	buf := make([]byte, 10)
	for i := 0; i < 5; i++ {
		if _, err := body.Read(buf); err != nil {
			t.Fatalf("read %d failed: %v", i, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
	if ctx.Err() != nil {
		t.Fatal("backend request was cancelled while the consumer was slow but the backend was not")
	}
}

func TestIdleTimeoutBody_CloseCancelsRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	body := newIdleTimeoutBody(io.NopCloser(strings.NewReader("data")), time.Minute, cancel)

	if err := body.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}
	if ctx.Err() == nil {
		t.Fatal("closing the body did not cancel the backend request")
	}
}

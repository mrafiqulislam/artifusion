package middleware

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func startServer(t *testing.T, handler http.Handler, readTimeout, writeTimeout time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.Config.ReadTimeout = readTimeout
	srv.Config.WriteTimeout = writeTimeout
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func slowDownloadHandler(chunks int, chunkSize int, interval time.Duration) http.Handler {
	chunk := bytes.Repeat([]byte("x"), chunkSize)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(chunks*chunkSize))
		w.WriteHeader(http.StatusOK)
		for i := 0; i < chunks; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(interval)
		}
	})
}

func TestStreamDeadlines_ActiveDownloadOutlivesWriteTimeout(t *testing.T) {
	const chunks, chunkSize = 15, 16 * 1024
	cfg := StreamConfig{IdleTimeout: time.Second}

	tests := []struct {
		name     string
		wrap     bool
		wantFull bool
	}{
		{name: "without stream deadlines the write timeout cuts the download", wrap: false, wantFull: false},
		{name: "with stream deadlines the download completes", wrap: true, wantFull: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var handler = slowDownloadHandler(chunks, chunkSize, 100*time.Millisecond)
			if tt.wrap {
				handler = StreamDeadlines(cfg, zerolog.Nop())(handler)
			}
			srv := startServer(t, handler, 10*time.Second, 500*time.Millisecond)

			resp, err := http.Get(srv.URL)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()
			body, readErr := io.ReadAll(resp.Body)

			full := readErr == nil && len(body) == chunks*chunkSize
			if full != tt.wantFull {
				t.Fatalf("full download = %v (read %d bytes, err %v), want %v", full, len(body), readErr, tt.wantFull)
			}
		})
	}
}

func TestStreamDeadlines_StalledClientIsCut(t *testing.T) {
	writeErr := make(chan error, 1)
	chunk := bytes.Repeat([]byte("x"), 64*1024)
	handler := StreamDeadlines(StreamConfig{IdleTimeout: 300 * time.Millisecond}, zerolog.Nop())(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			for i := 0; i < 4096; i++ {
				if _, err := w.Write(chunk); err != nil {
					writeErr <- err
					return
				}
			}
			writeErr <- nil
		}))
	srv := startServer(t, handler, 10*time.Second, 0)

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: test\r\n\r\n"); err != nil {
		t.Fatalf("write request failed: %v", err)
	}

	select {
	case err := <-writeErr:
		if err == nil {
			t.Fatal("handler wrote everything to a client that never reads")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stalled client was not cut by the idle timeout")
	}
}

func TestStreamDeadlines_TooSlowTransferIsCut(t *testing.T) {
	cfg := StreamConfig{IdleTimeout: time.Second, MinRate: 10_000, MinRateWindow: 300 * time.Millisecond}
	writeErr := make(chan error, 1)
	handler := StreamDeadlines(cfg, zerolog.Nop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 40; i++ {
			if _, err := w.Write([]byte("0123456789")); err != nil {
				writeErr <- err
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		writeErr <- nil
	}))

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if err := <-writeErr; !errors.Is(err, ErrTransferTooSlow) {
		t.Fatalf("got %v, want ErrTransferTooSlow", err)
	}
}

func TestStreamDeadlines_FastTransferIsNotCut(t *testing.T) {
	cfg := StreamConfig{IdleTimeout: time.Second, MinRate: 1_000, MinRateWindow: 100 * time.Millisecond}
	handler := StreamDeadlines(cfg, zerolog.Nop())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 20; i++ {
			if _, err := w.Write(bytes.Repeat([]byte("x"), 1024)); err != nil {
				t.Errorf("write %d failed: %v", i, err)
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Body.Len() != 20*1024 {
		t.Fatalf("got %d bytes, want %d", rec.Body.Len(), 20*1024)
	}
}

func TestStreamDeadlines_ActiveUploadOutlivesReadTimeout(t *testing.T) {
	const chunks, chunkSize = 15, 1024
	cfg := StreamConfig{IdleTimeout: time.Second}

	tests := []struct {
		name     string
		wrap     bool
		wantFull bool
	}{
		{name: "without stream deadlines the read timeout cuts the upload", wrap: false, wantFull: false},
		{name: "with stream deadlines the upload completes", wrap: true, wantFull: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			received := make(chan int, 1)
			var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				received <- len(body)
			})
			if tt.wrap {
				handler = StreamDeadlines(cfg, zerolog.Nop())(handler)
			}
			srv := startServer(t, handler, 500*time.Millisecond, 10*time.Second)

			pr, pw := io.Pipe()
			go func() {
				chunk := strings.Repeat("y", chunkSize)
				for i := 0; i < chunks; i++ {
					if _, err := pw.Write([]byte(chunk)); err != nil {
						return
					}
					time.Sleep(100 * time.Millisecond)
				}
				_ = pw.Close()
			}()

			req, _ := http.NewRequest(http.MethodPut, srv.URL, pr)
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				resp.Body.Close()
			}

			select {
			case n := <-received:
				if full := n == chunks*chunkSize; full != tt.wantFull {
					t.Fatalf("full upload = %v (received %d bytes), want %v", full, n, tt.wantFull)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("handler did not finish")
			}
		})
	}
}

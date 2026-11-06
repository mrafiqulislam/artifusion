package middleware

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"time"
)

func StreamDeadlines(idle time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if idle <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := http.NewResponseController(w)

			if r.Body != nil && r.Body != http.NoBody {
				r.Body = &idleDeadlineBody{ReadCloser: r.Body, rc: rc, idle: idle}
			}

			next.ServeHTTP(&progressWriter{ResponseWriter: w, rc: rc, idle: idle}, r)
		})
	}
}

type idleDeadlineBody struct {
	io.ReadCloser
	rc   *http.ResponseController
	idle time.Duration
}

func (b *idleDeadlineBody) Read(p []byte) (int, error) {
	_ = b.rc.SetReadDeadline(time.Now().Add(b.idle))
	return b.ReadCloser.Read(p)
}

type progressWriter struct {
	http.ResponseWriter
	rc   *http.ResponseController
	idle time.Duration
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	_ = pw.rc.SetWriteDeadline(time.Now().Add(pw.idle))
	return pw.ResponseWriter.Write(p)
}

func (pw *progressWriter) Flush() {
	_ = pw.rc.SetWriteDeadline(time.Now().Add(pw.idle))
	_ = pw.rc.Flush()
}

func (pw *progressWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return pw.rc.Hijack()
}

func (pw *progressWriter) Unwrap() http.ResponseWriter {
	return pw.ResponseWriter
}

package middleware

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

var ErrTransferTooSlow = errors.New("transfer rate below minimum")

type StreamConfig struct {
	IdleTimeout   time.Duration
	MinRate       int64
	MinRateWindow time.Duration
}

func StreamDeadlines(cfg StreamConfig, logger zerolog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if cfg.IdleTimeout <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := http.NewResponseController(w)

			if r.Body != nil && r.Body != http.NoBody {
				r.Body = &idleDeadlineBody{ReadCloser: r.Body, rc: rc, idle: cfg.IdleTimeout}
			}

			pw := &progressWriter{
				ResponseWriter: w,
				rc:             rc,
				cfg:            cfg,
				windowStart:    time.Now(),
				logger:         logger,
				path:           r.URL.Path,
			}
			next.ServeHTTP(pw, r)
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
	rc          *http.ResponseController
	cfg         StreamConfig
	windowStart time.Time
	windowBytes int64
	tooSlow     bool
	logger      zerolog.Logger
	path        string
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	if err := pw.checkRate(); err != nil {
		return 0, err
	}
	_ = pw.rc.SetWriteDeadline(time.Now().Add(pw.cfg.IdleTimeout))
	n, err := pw.ResponseWriter.Write(p)
	pw.windowBytes += int64(n)
	return n, err
}

func (pw *progressWriter) checkRate() error {
	if pw.tooSlow {
		return ErrTransferTooSlow
	}
	if pw.cfg.MinRate <= 0 || pw.cfg.MinRateWindow <= 0 {
		return nil
	}
	elapsed := time.Since(pw.windowStart)
	if elapsed < pw.cfg.MinRateWindow {
		return nil
	}
	if float64(pw.windowBytes) < float64(pw.cfg.MinRate)*elapsed.Seconds() {
		pw.tooSlow = true
		pw.logger.Warn().
			Str("path", pw.path).
			Int64("window_bytes", pw.windowBytes).
			Dur("window", elapsed).
			Int64("min_rate", pw.cfg.MinRate).
			Msg("Terminating transfer below minimum rate")
		return ErrTransferTooSlow
	}
	pw.windowStart = time.Now()
	pw.windowBytes = 0
	return nil
}

func (pw *progressWriter) Flush() {
	_ = pw.rc.SetWriteDeadline(time.Now().Add(pw.cfg.IdleTimeout))
	_ = pw.rc.Flush()
}

func (pw *progressWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return pw.rc.Hijack()
}

func (pw *progressWriter) Unwrap() http.ResponseWriter {
	return pw.ResponseWriter
}

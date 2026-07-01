package middleware

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/golang-jwt/jwt/v5"
)

type responseWriterWriter struct {
	http.ResponseWriter
	status       int
	bytesWritten int
	wroteHeader  bool
}

func (rw *responseWriterWriter) WriteHeader(code int) {
	if rw.wroteHeader {
		return
	}
	rw.status = code
	rw.wroteHeader = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriterWriter) Write(b []byte) (int, error) {
	if !rw.wroteHeader {
		rw.WriteHeader(http.StatusOK)
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

func (rw *responseWriterWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hijacker, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hijacker.Hijack()
	}
	return nil, nil, fmt.Errorf("responseWriter does not implement http.Hijacker")
}

func (rw *responseWriterWriter) Flush() {
	if flusher, ok := rw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// StructuredLogger is a custom HTTP middleware that outputs request execution logs in standard JSON format.
func StructuredLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		ww := &responseWriterWriter{
			ResponseWriter: w,
			status:         http.StatusOK,
		}

		defer func() {
			if ww.status < 400 {
				return // Silence success access logs to save data storage
			}

			latency := time.Since(start)
			reqID := middleware.GetReqID(r.Context())

			// Extract tenant and role from headers/token unverified to log it with context
			tenantID, role := extractTenantAndRole(r)

			level := slog.LevelWarn
			if ww.status >= 500 {
				level = slog.LevelError
			}

			slog.Log(
				r.Context(),
				level,
				"HTTP request completed",
				"request_id", reqID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.status,
				"bytes", ww.bytesWritten,
				"latency_ms", float64(latency.Nanoseconds())/1e6,
				"ip", r.RemoteAddr,
				"tenant_id", tenantID,
				"role", role,
			)
		}()

		next.ServeHTTP(ww, r)
	})
}

func extractTenantAndRole(r *http.Request) (tenantID string, role string) {
	tenantID = r.Header.Get("x-tenant-id")
	authHeader := r.Header.Get("Authorization")
	if strings.HasPrefix(authHeader, "VC ") {
		parts := strings.Split(authHeader, " ")
		if len(parts) == 2 {
			claims := jwt.MapClaims{}
			parser := jwt.NewParser()
			_, _, err := parser.ParseUnverified(parts[1], &claims)
			if err == nil {
				if tID, ok := claims["tenant_id"].(string); ok && tID != "" {
					tenantID = tID
				}
				if rVal, ok := claims["role"].(string); ok {
					role = rVal
				}
			}
		}
	}
	return tenantID, role
}

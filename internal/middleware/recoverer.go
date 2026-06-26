package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"atlas-api/internal/response"

	"github.com/go-chi/chi/v5/middleware"
)

// StructuredRecoverer is a middleware that recovers from panics, logs the panic and stack trace as JSON, and returns a 500 error response.
func StructuredRecoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rvr := recover(); rvr != nil {
				if rvr == http.ErrAbortHandler {
					// Go HTTP server uses this to abort a request, re-panic it so server handles it
					panic(rvr)
				}

				reqID := middleware.GetReqID(r.Context())
				stackTrace := string(debug.Stack())

				slog.Error(
					"server panic recovered",
					"request_id", reqID,
					"error", rvr,
					"stack", stackTrace,
				)

				response.Error(w, http.StatusInternalServerError, "Internal Server Error")
			}
		}()

		next.ServeHTTP(w, r)
	})
}

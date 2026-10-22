// Package api wires the HTTP routes of the control plane.
package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/molirain/molisync/server/internal/auth"
	"github.com/molirain/molisync/server/internal/ws"
)

// Deps are the collaborators required by the HTTP surface.
type Deps struct {
	Auth   *auth.Middleware
	Hub    *ws.Hub
	Logger *slog.Logger
}

// NewRouter builds the complete HTTP handler, including the middleware chain.
func NewRouter(deps Deps) http.Handler {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.Handle("GET /api/v1/me", deps.Auth.RequireDevice(http.HandlerFunc(handleMe)))
	mux.Handle("GET /api/v1/devices", deps.Auth.RequireDevice(http.HandlerFunc(handleDevices(deps.Hub))))
	mux.Handle("GET /api/v1/ws", deps.Auth.RequireDevice(deps.Hub))
	mux.HandleFunc("/", handleNotFound)

	return recoverPanics(logger, securityHeaders(accessLog(logger, mux)))
}

type healthResponse struct {
	Status string `json:"status"`
	Time   int64  `json:"time"`
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Time: time.Now().UnixMilli()})
}

type meResponse struct {
	DeviceID        string `json:"device_id"`
	CommonName      string `json:"common_name,omitempty"`
	Fingerprint     string `json:"fingerprint,omitempty"`
	ProxyIP         string `json:"proxy_ip"`
	AuthenticatedAt int64  `json:"authenticated_at"`
}

func handleMe(w http.ResponseWriter, r *http.Request) {
	identity, ok := auth.FromContext(r.Context())
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
		return
	}
	writeJSON(w, http.StatusOK, meResponse{
		DeviceID:        identity.DeviceID,
		CommonName:      identity.CommonName,
		Fingerprint:     identity.Fingerprint,
		ProxyIP:         identity.ProxyIP.String(),
		AuthenticatedAt: identity.AuthenticatedAt.UnixMilli(),
	})
}

type devicesResponse struct {
	Devices []ws.DeviceView `json:"devices"`
}

func handleDevices(hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		devices := hub.Devices()
		if devices == nil {
			devices = []ws.DeviceView{}
		}
		writeJSON(w, http.StatusOK, devicesResponse{Devices: devices})
	}
}

func handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeAPIError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no route for %s %s", r.Method, r.URL.Path))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func accessLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", recorder.bytes,
			"duration_ms", time.Since(started).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			if recovered == http.ErrAbortHandler {
				panic(recovered)
			}
			logger.Error("panic while serving request",
				"error", fmt.Sprint(recovered),
				"path", r.URL.Path,
				"stack", string(debug.Stack()),
			)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		}()
		next.ServeHTTP(w, r)
	})
}

// responseRecorder captures the status code without hiding the optional
// interfaces the WebSocket upgrade depends on.
type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	written, err := r.ResponseWriter.Write(data)
	r.bytes += int64(written)
	return written, err
}

func (r *responseRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (r *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (r *responseRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	body := struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{}
	body.Error.Code = code
	body.Error.Message = message
	writeJSON(w, status, body)
}

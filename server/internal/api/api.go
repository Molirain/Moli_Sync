// Package api wires the HTTP routes of the control plane.
package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/molirain/molisync/server/internal/auth"
	"github.com/molirain/molisync/server/internal/store"
	"github.com/molirain/molisync/server/internal/ws"
)

// Deps are the collaborators required by the HTTP surface.
type Deps struct {
	Auth   *auth.Middleware
	Hub    *ws.Hub
	Store  *store.Store
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
	mux.Handle("GET /api/v1/records", deps.Auth.RequireDevice(http.HandlerFunc(handleListRecords(deps))))
	mux.Handle("POST /api/v1/records/text", deps.Auth.RequireDevice(http.HandlerFunc(handleCreateText(deps))))
	mux.Handle("POST /api/v1/records/blob", deps.Auth.RequireDevice(http.HandlerFunc(handleCreateBlob(deps))))
	mux.Handle("GET /api/v1/blobs/{id}", deps.Auth.RequireDevice(http.HandlerFunc(handleBlob(deps))))
	mux.Handle("DELETE /api/v1/records/{id}", deps.Auth.RequireDevice(http.HandlerFunc(handleDeleteRecord(deps))))
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

type recordsResponse struct {
	Records []store.Record `json:"records"`
}

type createTextRequest struct {
	Text string `json:"text"`
}

func handleCreateText(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := auth.FromContext(r.Context())
		if !ok {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
			return
		}
		if deps.Store == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "storage_unavailable", "record storage is not configured")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, store.MaxTextBytes+1024)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		var payload createTextRequest
		if err := decoder.Decode(&payload); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "body must be {\"text\": \"...\"}")
			return
		}
		if strings.TrimSpace(payload.Text) == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "text must not be empty")
			return
		}

		record, err := deps.Store.SaveText(
			r.Context(), identity.DeviceID, senderName(deps, identity), payload.Text,
		)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		deps.Hub.BroadcastRecord(record)
		writeJSON(w, http.StatusCreated, record)
	}
}

func handleCreateBlob(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := auth.FromContext(r.Context())
		if !ok {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
			return
		}
		if deps.Store == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "storage_unavailable", "record storage is not configured")
			return
		}

		query := r.URL.Query()
		kind := strings.ToLower(strings.TrimSpace(query.Get("kind")))
		if kind != store.KindImage && kind != store.KindFile {
			writeAPIError(w, http.StatusBadRequest, "invalid_kind", "kind must be image or file")
			return
		}
		mimeType := strings.TrimSpace(query.Get("mime"))
		if mimeType == "" {
			mimeType = r.Header.Get("Content-Type")
		}
		blake3 := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Blake3")))
		if blake3 != "" && !isBlake3Hex(blake3) {
			writeAPIError(w, http.StatusBadRequest, "invalid_blake3", "X-Blake3 must be 64 lowercase hex characters")
			return
		}

		maxBytes := deps.Store.MaxItemBytes()
		if r.ContentLength > maxBytes {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "too_large", "upload exceeds the configured item limit")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1)

		record, err := deps.Store.SaveBlob(
			r.Context(),
			kind,
			identity.DeviceID,
			senderName(deps, identity),
			query.Get("name"),
			mimeType,
			blake3,
			r.ContentLength,
			r.Body,
		)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeAPIError(w, http.StatusRequestEntityTooLarge, "too_large", "upload exceeds the configured item limit")
				return
			}
			writeStoreError(w, err)
			return
		}
		deps.Hub.BroadcastRecord(record)
		writeJSON(w, http.StatusCreated, record)
	}
}

func handleListRecords(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Store == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "storage_unavailable", "record storage is not configured")
			return
		}
		query := r.URL.Query()
		kind := strings.ToLower(strings.TrimSpace(query.Get("kind")))
		limit := 50
		if raw := query.Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 200 {
				writeAPIError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200")
				return
			}
			limit = parsed
		}
		var before int64
		if raw := query.Get("before"); raw != "" {
			parsed, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || parsed < 0 {
				writeAPIError(w, http.StatusBadRequest, "invalid_before", "before must be a Unix millisecond timestamp")
				return
			}
			before = parsed
		}

		records, err := deps.Store.List(r.Context(), kind, limit, before)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, recordsResponse{Records: records})
	}
}

func handleBlob(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Store == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "storage_unavailable", "record storage is not configured")
			return
		}
		record, err := deps.Store.Get(r.Context(), r.PathValue("id"))
		if errors.Is(err, store.ErrNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "record not found")
			return
		}
		if err != nil {
			writeStoreError(w, err)
			return
		}
		if record.Kind == store.KindText || record.BlobID == "" {
			writeAPIError(w, http.StatusNotFound, "not_found", "record has no blob")
			return
		}

		file, err := deps.Store.OpenBlob(record)
		if errors.Is(err, store.ErrNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "blob not found")
			return
		}
		if err != nil {
			writeStoreError(w, err)
			return
		}
		defer file.Close()

		info, err := file.Stat()
		if err != nil {
			writeStoreError(w, err)
			return
		}

		contentType := record.MimeType
		if contentType == "" || strings.ContainsFunc(contentType, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			contentType = "application/octet-stream"
		}
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": record.FileName})
		if disposition == "" {
			disposition = "attachment"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", disposition)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, record.FileName, info.ModTime(), file)
	}
}

func handleDeleteRecord(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		identity, ok := auth.FromContext(r.Context())
		if !ok {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "authentication is required")
			return
		}
		if deps.Store == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "storage_unavailable", "record storage is not configured")
			return
		}
		err := deps.Store.Delete(r.Context(), r.PathValue("id"), identity.DeviceID)
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeAPIError(w, http.StatusNotFound, "not_found", "record not found")
		case errors.Is(err, store.ErrForbidden):
			writeAPIError(w, http.StatusForbidden, "forbidden", "record belongs to another device")
		case err != nil:
			writeStoreError(w, err)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

func senderName(deps Deps, identity auth.Identity) string {
	if deps.Hub != nil {
		if name := deps.Hub.DeviceName(identity.DeviceID); name != "" {
			return name
		}
	}
	if identity.CommonName != "" {
		return identity.CommonName
	}
	return "unknown"
}

func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrTooLarge):
		writeAPIError(w, http.StatusRequestEntityTooLarge, "too_large", "item exceeds the configured size limit")
	case errors.Is(err, store.ErrInvalidKind):
		writeAPIError(w, http.StatusBadRequest, "invalid_kind", "unknown record kind")
	case errors.Is(err, store.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "not_found", "record not found")
	default:
		writeAPIError(w, http.StatusInternalServerError, "storage_error", "cannot access record storage")
	}
}

func isBlake3Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
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

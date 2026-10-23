package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/molirain/molisync/server/internal/store"
	"github.com/molirain/molisync/server/internal/thumbnail"
)

// handleBlobThumbnail serves a small JPEG preview of an image record. Clients
// use it in list views so browsing a gallery never downloads the full file.
func handleBlobThumbnail(deps Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.Store == nil || deps.Thumbs == nil {
			writeAPIError(w, http.StatusServiceUnavailable, "thumbnail_unavailable", "preview rendering is not configured")
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
		if record.Kind != store.KindImage || record.BlobID == "" {
			writeAPIError(w, http.StatusNotFound, "not_found", "record has no image blob")
			return
		}

		width := thumbnail.DefaultWidth
		if raw := strings.TrimSpace(r.URL.Query().Get("w")); raw != "" {
			parsed, convErr := strconv.Atoi(raw)
			if convErr != nil || parsed < thumbnail.MinWidth || parsed > thumbnail.MaxWidth {
				writeAPIError(w, http.StatusBadRequest, "invalid_width",
					"w must be between "+strconv.Itoa(thumbnail.MinWidth)+" and "+strconv.Itoa(thumbnail.MaxWidth))
				return
			}
			width = parsed
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

		preview, err := deps.Thumbs.Render(record.BlobID, width, file)
		switch {
		case errors.Is(err, thumbnail.ErrUnsupported):
			writeAPIError(w, http.StatusUnsupportedMediaType, "unsupported_media", "blob is not a supported image")
			return
		case errors.Is(err, thumbnail.ErrTooLarge):
			writeAPIError(w, http.StatusUnsupportedMediaType, "image_too_large", "image is too large for a preview")
			return
		case err != nil:
			if deps.Logger != nil {
				deps.Logger.Warn("thumbnail render failed",
					slog.String("record_id", record.RecordID),
					slog.String("blob_id", record.BlobID),
					slog.Any("error", err))
			}
			writeAPIError(w, http.StatusUnsupportedMediaType, "unsupported_media", "blob is not a supported image")
			return
		}

		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.Itoa(len(preview)))
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(preview)
	}
}

package api

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/molirain/molisync/server/internal/store"
	"github.com/molirain/molisync/server/internal/thumbnail"
)

func newThumbDeps(t *testing.T) Deps {
	t.Helper()
	deps := testDeps(t)

	root := t.TempDir()
	records, err := store.Open(store.Options{DataDir: filepath.Join(root, "data")})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = records.Close() })

	cache, err := thumbnail.Open(filepath.Join(root, "thumbs"))
	if err != nil {
		t.Fatalf("thumbnail.Open: %v", err)
	}

	deps.Store = records
	deps.Thumbs = cache
	return deps
}

func sampleJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: uint8(x % 200), B: 90, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buffer.Bytes()
}

func thumbHeaders() map[string]string {
	return map[string]string{
		"X-Client-Verify":      "SUCCESS",
		"X-Client-Fingerprint": strings.Repeat("ab", 32),
		"X-Client-DN":          "CN=desk,O=Molirain",
	}
}

func TestBlobThumbnailRendersPreview(t *testing.T) {
	deps := newThumbDeps(t)
	jpegData := sampleJPEG(t, 900, 600)
	record, err := deps.Store.SaveBlob(
		context.Background(), store.KindImage, "device-a", "Desk",
		"photo.jpg", "image/jpeg", "", int64(len(jpegData)), bytes.NewReader(jpegData),
	)
	if err != nil {
		t.Fatalf("SaveBlob: %v", err)
	}

	router := NewRouter(deps)
	path := "/api/v1/blobs/" + record.RecordID + "/thumb?w=200"
	recorder := performRequest(t, router, http.MethodGet, path, "127.0.0.1:9000", thumbHeaders())
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q", got)
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(recorder.Body.Bytes()))
	if err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if format != "jpeg" || config.Width != 200 {
		t.Fatalf("unexpected preview %s %dx%d", format, config.Width, config.Height)
	}
}

func TestBlobThumbnailRejectsNonImageRecords(t *testing.T) {
	deps := newThumbDeps(t)
	record, err := deps.Store.SaveBlob(
		context.Background(), store.KindFile, "device-a", "Desk",
		"a.bin", "application/octet-stream", "", 4, bytes.NewReader([]byte("data")),
	)
	if err != nil {
		t.Fatalf("SaveBlob: %v", err)
	}

	router := NewRouter(deps)
	recorder := performRequest(t, router, http.MethodGet,
		"/api/v1/blobs/"+record.RecordID+"/thumb", "127.0.0.1:9000", thumbHeaders())
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestBlobThumbnailRejectsBrokenImageData(t *testing.T) {
	deps := newThumbDeps(t)
	broken := []byte("notajpeg")
	record, err := deps.Store.SaveBlob(
		context.Background(), store.KindImage, "device-a", "Desk",
		"broken.jpg", "image/jpeg", "", int64(len(broken)), bytes.NewReader(broken),
	)
	if err != nil {
		t.Fatalf("SaveBlob: %v", err)
	}

	router := NewRouter(deps)
	recorder := performRequest(t, router, http.MethodGet,
		"/api/v1/blobs/"+record.RecordID+"/thumb", "127.0.0.1:9000", thumbHeaders())
	if recorder.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestBlobThumbnailRejectsOutOfRangeWidth(t *testing.T) {
	deps := newThumbDeps(t)
	jpegData := sampleJPEG(t, 300, 300)
	record, err := deps.Store.SaveBlob(
		context.Background(), store.KindImage, "device-a", "Desk",
		"photo.jpg", "image/jpeg", "", int64(len(jpegData)), bytes.NewReader(jpegData),
	)
	if err != nil {
		t.Fatalf("SaveBlob: %v", err)
	}

	router := NewRouter(deps)
	for _, width := range []string{"abc", "10", "5000"} {
		recorder := performRequest(t, router, http.MethodGet,
			"/api/v1/blobs/"+record.RecordID+"/thumb?w="+width, "127.0.0.1:9000", thumbHeaders())
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("w=%s: status = %d, body = %s", width, recorder.Code, recorder.Body.String())
		}
	}
}

func TestBlobThumbnailReusesCache(t *testing.T) {
	deps := newThumbDeps(t)
	jpegData := sampleJPEG(t, 400, 400)
	record, err := deps.Store.SaveBlob(
		context.Background(), store.KindImage, "device-a", "Desk",
		"photo.jpg", "image/jpeg", "", int64(len(jpegData)), bytes.NewReader(jpegData),
	)
	if err != nil {
		t.Fatalf("SaveBlob: %v", err)
	}

	router := NewRouter(deps)
	path := "/api/v1/blobs/" + record.RecordID + "/thumb"
	first := performRequest(t, router, http.MethodGet, path, "127.0.0.1:9000", thumbHeaders())
	if first.Code != http.StatusOK {
		t.Fatalf("status = %d", first.Code)
	}
	if _, ok := deps.Thumbs.Get(record.BlobID, thumbnail.DefaultWidth); !ok {
		t.Fatal("expected the preview to be cached on disk")
	}

	second := performRequest(t, router, http.MethodGet, path, "127.0.0.1:9000", thumbHeaders())
	if second.Code != http.StatusOK || !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Fatal("expected a stable cached preview")
	}
}

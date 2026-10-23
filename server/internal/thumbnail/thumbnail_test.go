package thumbnail

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func encodeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buffer.Bytes()
}

func TestRenderDownscalesToRequestedWidth(t *testing.T) {
	source := encodeJPEG(t, 640, 480)

	preview, err := Render(bytes.NewReader(source), 160)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(preview))
	if err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("expected jpeg preview, got %s", format)
	}
	if config.Width != 160 {
		t.Fatalf("expected width 160, got %d", config.Width)
	}
	if config.Height != 120 {
		t.Fatalf("expected height 120 (aspect preserved), got %d", config.Height)
	}
}

func TestRenderKeepsSmallImagesAtNativeSize(t *testing.T) {
	source := encodeJPEG(t, 100, 100)

	preview, err := Render(bytes.NewReader(source), 320)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	config, _, err := image.DecodeConfig(bytes.NewReader(preview))
	if err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if config.Width != 100 || config.Height != 100 {
		t.Fatalf("small image must not be upscaled, got %dx%d", config.Width, config.Height)
	}
}

func TestRenderAcceptsPNG(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 300, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 300; x++ {
			img.Set(x, y, color.NRGBA{R: 10, G: 200, B: 40, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	preview, err := Render(bytes.NewReader(buffer.Bytes()), 128)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if len(preview) == 0 {
		t.Fatal("expected preview bytes")
	}
}

func TestRenderRejectsNonImages(t *testing.T) {
	_, err := Render(bytes.NewReader([]byte("definitely not an image")), 320)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

func TestClampWidth(t *testing.T) {
	cases := map[int]int{
		0:    DefaultWidth,
		-5:   DefaultWidth,
		10:   MinWidth,
		200:  200,
		9999: MaxWidth,
	}
	for input, expected := range cases {
		if got := ClampWidth(input); got != expected {
			t.Fatalf("ClampWidth(%d) = %d, want %d", input, got, expected)
		}
	}
}

func TestCacheReusesRenderedPreview(t *testing.T) {
	cache, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	source := encodeJPEG(t, 800, 600)

	first, err := cache.Render("blob1", 200, bytes.NewReader(source))
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if _, ok := cache.Get("blob1", 200); !ok {
		t.Fatal("expected the preview to be cached")
	}

	// A second render must not need the source at all.
	second, err := cache.Render("blob1", 200, bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("cached Render: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("cached preview differs from the freshly rendered one")
	}
}

func TestCacheSerialisesConcurrentRenders(t *testing.T) {
	dir := t.TempDir()
	cache, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	source := encodeJPEG(t, 512, 512)

	var wg sync.WaitGroup
	var mu sync.Mutex
	sizes := make([][]byte, 8)
	for i := range sizes {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			preview, renderErr := cache.Render("blob2", 128, bytes.NewReader(source))
			if renderErr != nil {
				t.Errorf("Render: %v", renderErr)
				return
			}
			mu.Lock()
			sizes[index] = preview
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	for index := 1; index < len(sizes); index++ {
		if !bytes.Equal(sizes[0], sizes[index]) {
			t.Fatal("concurrent renders produced different previews")
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one cache file, got %d", len(entries))
	}
}

func TestInvalidateRemovesEverySize(t *testing.T) {
	cache, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	source := encodeJPEG(t, 400, 400)
	for _, width := range []int{128, 256} {
		if _, renderErr := cache.Render("blob3", width, bytes.NewReader(source)); renderErr != nil {
			t.Fatalf("Render: %v", renderErr)
		}
	}

	cache.Invalidate("blob3")

	if _, ok := cache.Get("blob3", 128); ok {
		t.Fatal("expected the 128px preview to be removed")
	}
	if _, ok := cache.Get("blob3", 256); ok {
		t.Fatal("expected the 256px preview to be removed")
	}
	if _, err := os.Stat(filepath.Join(cache.Dir(), "blob3-128.jpg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected the cache file to be gone, got %v", err)
	}
}

func TestIsSupported(t *testing.T) {
	if !IsSupported("image/jpeg") || !IsSupported("image/png") || !IsSupported("image/gif") {
		t.Fatal("expected common image types to be supported")
	}
	if IsSupported("application/pdf") || IsSupported("") {
		t.Fatal("expected non-image types to be rejected")
	}
}

// Package thumbnail renders and caches small JPEG previews for image blobs.
//
// Previews are generated with the standard library only: the decoder handles
// JPEG, PNG and GIF, and the scaler uses area averaging which produces stable
// results for large photos without pulling in a third-party imaging library.
package thumbnail

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // register the GIF decoder
	"image/jpeg"
	_ "image/png" // register the PNG decoder
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

// Supported sizes and quality are deliberately fixed so the cache stays small
// and clients cannot force an expensive render by asking for arbitrary sizes.
const (
	// MinWidth and MaxWidth clamp the requested preview width.
	MinWidth = 64
	MaxWidth = 1024
	// DefaultWidth is used when the client does not ask for a specific size.
	DefaultWidth = 320
	// DefaultQuality is the JPEG quality of every preview.
	DefaultQuality = 80
	// MaxPixels rejects images whose decoded bitmap would be too large to keep
	// in memory safely. Clients fall back to a full download in that case.
	MaxPixels = 24_000_000
	// maxSourceBytes bounds how much encoded data is buffered per render.
	maxSourceBytes = 32 << 20
)

var (
	// ErrUnsupported reports that the blob is not a decodable image.
	ErrUnsupported = errors.New("thumbnail: unsupported image format")
	// ErrTooLarge reports that the image exceeds MaxPixels.
	ErrTooLarge = errors.New("thumbnail: image is too large for a preview")
)

// Cache renders previews into a directory, reusing results across restarts.
type Cache struct {
	dir   string
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// Open prepares dir for preview storage.
func Open(dir string) (*Cache, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("thumbnail: resolve dir: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, fmt.Errorf("thumbnail: create dir: %w", err)
	}
	return &Cache{dir: abs, locks: make(map[string]*sync.Mutex)}, nil
}

// Dir returns the preview directory.
func (c *Cache) Dir() string { return c.dir }

// ClampWidth normalises a requested preview width.
func ClampWidth(width int) int {
	switch {
	case width <= 0:
		return DefaultWidth
	case width < MinWidth:
		return MinWidth
	case width > MaxWidth:
		return MaxWidth
	default:
		return width
	}
}

// Path returns the cache location for a key and width.
func (c *Cache) Path(key string, width int) string {
	return filepath.Join(c.dir, key+"-"+strconv.Itoa(ClampWidth(width))+".jpg")
}

// Get returns a cached preview, or false when it has not been rendered yet.
func (c *Cache) Get(key string, width int) ([]byte, bool) {
	data, err := os.ReadFile(c.Path(key, width))
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

// Render returns a JPEG preview no wider than width, rendering it from src when
// the cache is cold. Concurrent callers for the same key are serialised so a
// cold cache cannot decode the same photo several times at once.
func (c *Cache) Render(key string, width int, src io.Reader) ([]byte, error) {
	width = ClampWidth(width)
	if data, ok := c.Get(key, width); ok {
		return data, nil
	}

	lock := c.keyLock(key)
	lock.Lock()
	defer lock.Unlock()

	if data, ok := c.Get(key, width); ok {
		return data, nil
	}

	preview, err := Render(src, width)
	if err != nil {
		return nil, err
	}
	_ = c.write(key, width, preview)
	return preview, nil
}

// Invalidate drops every cached size for a key.
func (c *Cache) Invalidate(key string) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	prefix := key + "-"
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || len(name) <= len(prefix) || name[:len(prefix)] != prefix {
			continue
		}
		_ = os.Remove(filepath.Join(c.dir, name))
	}
}

func (c *Cache) keyLock(key string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	lock, ok := c.locks[key]
	if !ok {
		lock = &sync.Mutex{}
		c.locks[key] = lock
	}
	return lock
}

func (c *Cache) write(key string, width int, data []byte) error {
	path := c.Path(key, width)
	temporary := path + ".part"
	if err := os.WriteFile(temporary, data, 0o640); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

// Render decodes an image and returns a JPEG preview no wider than width.
func Render(src io.Reader, width int) ([]byte, error) {
	width = ClampWidth(width)
	data, err := io.ReadAll(io.LimitReader(src, maxSourceBytes))
	if err != nil {
		return nil, fmt.Errorf("thumbnail: read source: %w", err)
	}

	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, ErrUnsupported
	}
	if config.Width <= 0 || config.Height <= 0 {
		return nil, ErrUnsupported
	}
	if config.Width*config.Height > MaxPixels {
		return nil, ErrTooLarge
	}

	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("thumbnail: decode %s: %w", format, err)
	}

	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, scaleToWidth(decoded, width), &jpeg.Options{Quality: DefaultQuality}); err != nil {
		return nil, fmt.Errorf("thumbnail: encode: %w", err)
	}
	return buffer.Bytes(), nil
}

// IsSupported reports whether a MIME type looks like a decodable image.
func IsSupported(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/jpg", "image/png", "image/gif":
		return true
	default:
		return false
	}
}

// scaleToWidth shrinks an image to width using area averaging. Images that are
// already narrower are re-encoded unchanged instead of being upscaled.
func scaleToWidth(src image.Image, width int) image.Image {
	bounds := src.Bounds()
	srcWidth, srcHeight := bounds.Dx(), bounds.Dy()
	if srcWidth <= width {
		return src
	}

	height := int(float64(srcHeight) * float64(width) / float64(srcWidth))
	if height < 1 {
		height = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))

	xScale := float64(srcWidth) / float64(width)
	yScale := float64(srcHeight) / float64(height)
	for y := 0; y < height; y++ {
		y0 := bounds.Min.Y + int(float64(y)*yScale)
		y1 := bounds.Min.Y + int(float64(y+1)*yScale)
		if y1 <= y0 {
			y1 = y0 + 1
		}
		if y1 > bounds.Max.Y {
			y1 = bounds.Max.Y
		}
		for x := 0; x < width; x++ {
			x0 := bounds.Min.X + int(float64(x)*xScale)
			x1 := bounds.Min.X + int(float64(x+1)*xScale)
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if x1 > bounds.Max.X {
				x1 = bounds.Max.X
			}

			var red, green, blue, alpha, count uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, ca := src.At(sx, sy).RGBA()
					red += uint64(cr)
					green += uint64(cg)
					blue += uint64(cb)
					alpha += uint64(ca)
					count++
				}
			}
			if count == 0 {
				continue
			}
			offset := y*dst.Stride + x*4
			dst.Pix[offset] = uint8(red / count >> 8)
			dst.Pix[offset+1] = uint8(green / count >> 8)
			dst.Pix[offset+2] = uint8(blue / count >> 8)
			dst.Pix[offset+3] = uint8(alpha / count >> 8)
		}
	}
	return dst
}

// Package ephemeral detects data directories that live in a container's
// writable layer instead of a mounted volume.
//
// A Compose file that declares a volume but never mounts it into the service
// looks correct at a glance, yet every `docker compose up` after an image
// update recreates the container filesystem and silently drops the database.
// The control plane cannot prevent that, so it warns loudly at startup.
package ephemeral

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// mountInfoPath is a variable so tests can supply a fake table.
var mountInfoPath = "/proc/self/mountinfo"

// overlayFilesystems are the layered filesystems Docker uses for the writable
// container layer.
var overlayFilesystems = map[string]struct{}{
	"overlay":        {},
	"overlay2":       {},
	"aufs":           {},
	"fuse-overlayfs": {},
}

// Detect reports whether dir is stored on the container's writable layer.
//
// An error is returned when the mount table cannot be read, which is the case
// on non-Linux hosts; callers should treat that as "unknown" and stay quiet.
func Detect(dir string) (bool, error) {
	file, err := os.Open(mountInfoPath)
	if err != nil {
		return false, fmt.Errorf("ephemeral: open mount table: %w", err)
	}
	defer file.Close()
	return DetectFrom(dir, file)
}

// DetectFrom is Detect with an explicit mountinfo reader.
func DetectFrom(dir string, mountInfo io.Reader) (bool, error) {
	// Mount points in the table are always POSIX paths, so compare in that
	// space and never let Windows path semantics leak in.
	target := path.Clean(filepath.ToSlash(dir))
	if !strings.HasPrefix(target, "/") {
		return false, fmt.Errorf("ephemeral: %q is not an absolute path", dir)
	}
	mountPoint, fsType, err := lookup(target, mountInfo)
	if err != nil {
		return false, err
	}
	if mountPoint == "" {
		// No mount table entry covers the path (unusual); assume it is fine.
		return false, nil
	}
	_, overlay := overlayFilesystems[fsType]
	return overlay, nil
}

// lookup returns the longest mount point containing target and its filesystem.
func lookup(target string, mountInfo io.Reader) (string, string, error) {
	scanner := bufio.NewScanner(mountInfo)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var bestPoint, bestType string
	for scanner.Scan() {
		fields := strings.SplitN(scanner.Text(), " - ", 2)
		if len(fields) != 2 {
			continue
		}
		left := strings.Fields(fields[0])
		right := strings.Fields(fields[1])
		if len(left) < 5 || len(right) < 1 {
			continue
		}
		point := unescape(left[4])
		if point == "" || !contains(point, target) {
			continue
		}
		if len(point) > len(bestPoint) {
			bestPoint = point
			bestType = right[0]
		}
	}
	if err := scanner.Err(); err != nil {
		return "", "", fmt.Errorf("ephemeral: read mount table: %w", err)
	}
	return bestPoint, bestType, nil
}

// contains reports whether target is the mount point itself or lives under it.
func contains(mountPoint, target string) bool {
	if target == mountPoint {
		return true
	}
	prefix := mountPoint
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return strings.HasPrefix(target, prefix)
}

// unescape decodes the octal escapes (e.g. "\040" for a space) that mountinfo
// uses for paths containing whitespace or backslashes.
func unescape(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] == '\\' && index+3 < len(value) {
			var decoded byte
			if _, err := fmt.Sscanf(value[index+1:index+4], "%o", &decoded); err == nil {
				builder.WriteByte(decoded)
				index += 3
				continue
			}
		}
		builder.WriteByte(value[index])
	}
	return builder.String()
}

package ephemeral

import (
	"strings"
	"testing"
)

// mountinfo excerpt from a container whose data directory is not mounted.
const containerOnly = `22 1 0:21 / / rw,relatime - overlay overlay rw,lowerdir=/a:/b,upperdir=/c,workdir=/d
23 22 0:22 / /proc rw,nosuid,nodev,noexec,relatime - proc proc rw
24 22 0:23 / /dev rw,nosuid - tmpfs tmpfs rw,size=65536k
`

// The same container with a named volume mounted at the data directory.
const withVolume = containerOnly + `30 22 253:1 /var/lib/docker/volumes/molisync-data/_data /var/lib/molisync rw,relatime - ext4 /dev/vda1 rw
`

// A bind mount deeper than the data directory must not win the lookup.
const withNestedBind = containerOnly + `31 22 253:1 /host/data /var/lib/molisync/blobs rw,relatime - xfs /dev/vdb1 rw
`

func TestDetectTreatsContainerLayerAsEphemeral(t *testing.T) {
	got, err := DetectFrom("/var/lib/molisync", strings.NewReader(containerOnly))
	if err != nil {
		t.Fatalf("DetectFrom: %v", err)
	}
	if !got {
		t.Fatal("expected a data directory on the overlay root to be flagged")
	}
}

func TestDetectAcceptsMountedVolume(t *testing.T) {
	got, err := DetectFrom("/var/lib/molisync", strings.NewReader(withVolume))
	if err != nil {
		t.Fatalf("DetectFrom: %v", err)
	}
	if got {
		t.Fatal("a named volume at the data directory must not be flagged")
	}
}

func TestDetectUsesTheDeepestMountPoint(t *testing.T) {
	// /var/lib/molisync is on overlay, but /var/lib/molisync/blobs is a bind
	// mount. The data directory itself is still ephemeral.
	got, err := DetectFrom("/var/lib/molisync", strings.NewReader(withNestedBind))
	if err != nil {
		t.Fatalf("DetectFrom: %v", err)
	}
	if !got {
		t.Fatal("expected the data directory to stay flagged")
	}

	got, err = DetectFrom("/var/lib/molisync/blobs", strings.NewReader(withNestedBind))
	if err != nil {
		t.Fatalf("DetectFrom: %v", err)
	}
	if got {
		t.Fatal("the bind mounted subdirectory must not be flagged")
	}
}

func TestDetectIgnoresUnrelatedPrefixes(t *testing.T) {
	// "/var/lib/molisync-old" must not be treated as living under
	// "/var/lib/molisync".
	info := `22 1 0:21 / / rw,relatime - overlay overlay rw
40 22 253:1 /host/x /var/lib/molisync rw - ext4 /dev/vda1 rw
`
	got, err := DetectFrom("/var/lib/molisync-old/data", strings.NewReader(info))
	if err != nil {
		t.Fatalf("DetectFrom: %v", err)
	}
	if !got {
		t.Fatal("expected the sibling path to fall back to the overlay root")
	}
}

func TestDetectDecodesEscapedSpaces(t *testing.T) {
	info := `22 1 0:21 / / rw,relatime - overlay overlay rw
41 22 253:1 /host/my\040data /srv/my\040data rw - ext4 /dev/vda1 rw
`
	got, err := DetectFrom("/srv/my data", strings.NewReader(info))
	if err != nil {
		t.Fatalf("DetectFrom: %v", err)
	}
	if got {
		t.Fatal("expected the escaped mount point to be recognised")
	}
}

func TestDetectRejectsMalformedLines(t *testing.T) {
	info := "garbage\n44 22\n"
	got, err := DetectFrom("/var/lib/molisync", strings.NewReader(info))
	if err != nil {
		t.Fatalf("DetectFrom: %v", err)
	}
	if got {
		t.Fatal("malformed tables must not report a false positive")
	}
}

package store

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openClockStore(t *testing.T, clock *time.Time) *Store {
	t.Helper()
	instance, err := Open(Options{
		DataDir: filepath.Join(t.TempDir(), "data"),
		Now:     func() time.Time { return *clock },
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = instance.Close() })
	return instance
}

func TestDeviceKeyIsStableAndDoesNotContainTheFingerprint(t *testing.T) {
	store := openClockStore(t, ptrTime(time.Unix(1_700_000_000, 0)))

	first, err := store.DeviceKey("aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899")
	if err != nil {
		t.Fatalf("DeviceKey: %v", err)
	}
	second, err := store.DeviceKey("aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899")
	if err != nil {
		t.Fatalf("DeviceKey: %v", err)
	}
	other, err := store.DeviceKey("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatalf("DeviceKey: %v", err)
	}

	if first != second {
		t.Fatal("device keys must be stable")
	}
	if first == other {
		t.Fatal("different devices must not collide")
	}
	if len(first) != 64 {
		t.Fatalf("device key length = %d, want 64", len(first))
	}
}

func TestDeviceKeySurvivesReopenWithTheSameSalt(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "data")
	open := func() *Store {
		instance, err := Open(Options{DataDir: directory})
		if err != nil {
			t.Fatalf("open store: %v", err)
		}
		return instance
	}

	first := open()
	key, err := first.DeviceKey("device-a")
	if err != nil {
		t.Fatalf("DeviceKey: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second := open()
	defer second.Close()
	reopened, err := second.DeviceKey("device-a")
	if err != nil {
		t.Fatalf("DeviceKey: %v", err)
	}
	if key != reopened {
		t.Fatal("the salt must persist across restarts")
	}
}

func TestDeviceStatsBucketsByAgeAndPlatform(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0).UTC()
	store := openClockStore(t, &clock)
	ctx := context.Background()

	if _, _, err := store.TouchDevice(ctx, "device-windows", "windows"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if _, _, err := store.TouchDevice(ctx, "device-android", "android"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if _, _, err := store.TouchDevice(ctx, "device-linux", "linux"); err != nil {
		t.Fatalf("touch: %v", err)
	}

	// Touching twice must not double count.
	if _, _, err := store.TouchDevice(ctx, "device-windows", "windows"); err != nil {
		t.Fatalf("touch: %v", err)
	}

	stats, err := store.DeviceStats(ctx)
	if err != nil {
		t.Fatalf("DeviceStats: %v", err)
	}
	if stats.Total != 3 || stats.Active7d != 3 || stats.Active30d != 3 {
		t.Fatalf("unexpected counts: %+v", stats)
	}
	if stats.Windows != 1 || stats.Android != 1 || stats.Other != 1 {
		t.Fatalf("unexpected platform buckets: %+v", stats)
	}

	clock = clock.Add(10 * 24 * time.Hour)
	if _, _, err := store.TouchDevice(ctx, "device-android", "android"); err != nil {
		t.Fatalf("touch: %v", err)
	}

	stats, err = store.DeviceStats(ctx)
	if err != nil {
		t.Fatalf("DeviceStats: %v", err)
	}
	if stats.Total != 3 {
		t.Fatalf("total = %d, want 3", stats.Total)
	}
	if stats.Active7d != 1 {
		t.Fatalf("active_7d = %d, want 1", stats.Active7d)
	}
	if stats.Active30d != 3 {
		t.Fatalf("active_30d = %d, want 3", stats.Active30d)
	}
}

func TestRecordStatsCountsKindsAndBytes(t *testing.T) {
	store := newTestStore(t, 1<<20)
	ctx := context.Background()

	if _, err := store.SaveText(ctx, "device-a", "Desk", "hello"); err != nil {
		t.Fatalf("SaveText: %v", err)
	}
	if _, err := store.SaveBlob(
		ctx, KindImage, "device-a", "Desk", "a.png", "image/png", "", 10,
		bytes.NewReader([]byte("1234567890")),
	); err != nil {
		t.Fatalf("SaveBlob: %v", err)
	}

	stats, err := store.RecordStats(ctx)
	if err != nil {
		t.Fatalf("RecordStats: %v", err)
	}
	if stats.Text != 1 || stats.Image != 1 || stats.File != 0 {
		t.Fatalf("unexpected kinds: %+v", stats)
	}
	if stats.Bytes != 10 {
		t.Fatalf("bytes = %d, want 10", stats.Bytes)
	}
}

func TestSnapshotIncludesDeploymentIdentityAndFirstSeen(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0).UTC()
	store := openClockStore(t, &clock)
	ctx := context.Background()

	snapshot, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snapshot.DeploymentID) != 32 {
		t.Fatalf("deployment id = %q, want 32 hex characters", snapshot.DeploymentID)
	}
	if !snapshot.FirstSeen.Equal(clock) {
		t.Fatalf("first seen = %s, want %s", snapshot.FirstSeen, clock)
	}

	again, err := store.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if again.DeploymentID != snapshot.DeploymentID {
		t.Fatal("the deployment id must be stable")
	}
}

func TestTouchDeviceReportsOnlyTheFirstInstall(t *testing.T) {
	clock := time.Unix(1_700_000_000, 0).UTC()
	store := openClockStore(t, &clock)
	ctx := context.Background()

	key, created, err := store.TouchDevice(ctx, "device-a", "windows")
	if err != nil {
		t.Fatalf("TouchDevice: %v", err)
	}
	if !created {
		t.Fatal("the first connection must count as an installation")
	}
	if key == "" {
		t.Fatal("device key must be returned for the install report")
	}

	secondKey, created, err := store.TouchDevice(ctx, "device-a", "windows")
	if err != nil {
		t.Fatalf("TouchDevice: %v", err)
	}
	if created {
		t.Fatal("a reconnect must not count as another installation")
	}
	if secondKey != key {
		t.Fatal("the device key must be stable")
	}
}

func TestNormalizePlatform(t *testing.T) {
	cases := map[string]string{
		"windows":     PlatformWindows,
		"Windows":     PlatformWindows,
		"win":         PlatformWindows,
		"windows-x64": PlatformWindows,
		"android":     PlatformAndroid,
		"linux":       PlatformOther,
		"":            PlatformOther,
	}
	for input, want := range cases {
		if got := NormalizePlatform(input); got != want {
			t.Fatalf("NormalizePlatform(%q) = %q, want %q", input, got, want)
		}
	}
}

func ptrTime(value time.Time) *time.Time { return &value }

package store

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T, maxItemBytes int64) *Store {
	t.Helper()
	root := t.TempDir()
	instance, err := Open(Options{
		DataDir:      filepath.Join(root, "data"),
		MaxItemBytes: maxItemBytes,
		Now: func() time.Time {
			return time.UnixMilli(1_700_000_000_000)
		},
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = instance.Close()
	})
	return instance
}

func TestTextRecordRoundTrip(t *testing.T) {
	ctx := context.Background()
	instance := newTestStore(t, 1<<20)

	record, err := instance.SaveText(ctx, "device-a", "Phone A", "hello world")
	if err != nil {
		t.Fatalf("save text: %v", err)
	}
	if record.RecordID == "" || record.Kind != KindText || record.CreatedAt != 1_700_000_000_000 {
		t.Fatalf("unexpected record: %+v", record)
	}

	stored, err := instance.Get(ctx, record.RecordID)
	if err != nil {
		t.Fatalf("get record: %v", err)
	}
	if stored.Text != "hello world" || stored.SenderName != "Phone A" {
		t.Fatalf("unexpected stored record: %+v", stored)
	}

	if _, err := instance.SaveText(ctx, "device-a", "Phone A", ""); err == nil {
		t.Fatal("empty text must be rejected")
	}
}

func TestBlobRoundTrip(t *testing.T) {
	ctx := context.Background()
	instance := newTestStore(t, 1<<20)
	content := []byte("blob-content")

	record, err := instance.SaveBlob(
		ctx, KindFile, "device-a", "Phone A", "../photo.jpg", "image/jpeg", "", int64(len(content)),
		bytes.NewReader(content),
	)
	if err != nil {
		t.Fatalf("save blob: %v", err)
	}
	if record.FileName != "photo.jpg" {
		t.Fatalf("file name was not sanitized: %q", record.FileName)
	}
	if record.Size != int64(len(content)) || record.BlobID != record.RecordID {
		t.Fatalf("unexpected blob record: %+v", record)
	}

	file, err := instance.OpenBlob(record)
	if err != nil {
		t.Fatalf("open blob: %v", err)
	}
	defer file.Close()
	read, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read blob: %v", err)
	}
	if !bytes.Equal(read, content) {
		t.Fatalf("blob content mismatch: %q", read)
	}

	records, err := instance.List(ctx, KindFile, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(records) != 1 || records[0].RecordID != record.RecordID {
		t.Fatalf("unexpected list: %+v", records)
	}
	if records, err := instance.List(ctx, KindText, 10, 0); err != nil || len(records) != 0 {
		t.Fatalf("text list should be empty: %+v %v", records, err)
	}
}

func TestBlobSizeLimits(t *testing.T) {
	ctx := context.Background()
	instance := newTestStore(t, 8)

	_, err := instance.SaveBlob(ctx, KindFile, "device-a", "A", "big.bin", "", "", -1,
		bytes.NewReader(bytes.Repeat([]byte("x"), 9)))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(filepath.Dir(instance.blobDir), "blobs"))
	if err != nil {
		t.Fatalf("read blob dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial blob was not removed: %v", entries)
	}

	_, err = instance.SaveBlob(ctx, KindFile, "device-a", "A", "small.bin", "", "", 10,
		bytes.NewReader([]byte("12345")))
	if err == nil {
		t.Fatal("size mismatch must be rejected")
	}
	if _, err := instance.SaveBlob(ctx, KindImage, "a", "A", "empty.png", "", "", 0, bytes.NewReader(nil)); err == nil {
		t.Fatal("empty blob must be rejected")
	}
}

func TestDeleteEnforcesOwnership(t *testing.T) {
	ctx := context.Background()
	instance := newTestStore(t, 1<<20)
	record, err := instance.SaveText(ctx, "owner", "Owner", "secret")
	if err != nil {
		t.Fatalf("save: %v", err)
	}

	if err := instance.Delete(ctx, record.RecordID, "intruder"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
	if err := instance.Delete(ctx, record.RecordID, "owner"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := instance.Get(ctx, record.RecordID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestRecordsSurviveReopen(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")

	first, err := Open(Options{DataDir: dataDir, MaxItemBytes: 1 << 20})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	record, err := first.SaveText(ctx, "device", "Device", "persisted")
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := Open(Options{DataDir: dataDir, MaxItemBytes: 1 << 20})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	stored, err := second.Get(ctx, record.RecordID)
	if err != nil {
		t.Fatalf("get after reopen: %v", err)
	}
	if stored.Text != "persisted" {
		t.Fatalf("unexpected record after reopen: %+v", stored)
	}
}

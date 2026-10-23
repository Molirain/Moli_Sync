// Package store persists synced text records and blob metadata in SQLite and
// keeps blob bytes on disk. Repository data is intentionally simple: one row
// per record, one file per blob, no external object storage.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Record kinds. They mirror the sync_record payload_type values.
const (
	KindText  = "text"
	KindImage = "image"
	KindFile  = "file"
)

// ErrMetaNotFound reports a missing key in the meta table.
var ErrMetaNotFound = errors.New("store: meta key not found")

const (
	DefaultMaxItemBytes = 64 << 20
	MaxTextBytes        = 64 << 10
	maxListLimit        = 200
)

var (
	// ErrNotFound means the requested record or blob does not exist.
	ErrNotFound = errors.New("store: record not found")
	// ErrTooLarge means the uploaded item exceeds the configured limit.
	ErrTooLarge = errors.New("store: item exceeds the size limit")
	// ErrForbidden means the record belongs to another device.
	ErrForbidden = errors.New("store: record belongs to another device")
	// ErrInvalidKind means the payload kind is not text, image or file.
	ErrInvalidKind = errors.New("store: invalid record kind")
)

// Record is one synced item. Text records carry Text; image and file records
// carry blob metadata. The JSON shape matches the sync_record control frame.
type Record struct {
	RecordID       string `json:"record_id"`
	SenderDeviceID string `json:"sender_device_id"`
	SenderName     string `json:"sender_name,omitempty"`
	CreatedAt      int64  `json:"created_at"`
	Kind           string `json:"type"`
	Text           string `json:"text_content,omitempty"`
	BlobID         string `json:"blob_id,omitempty"`
	FileName       string `json:"file_name,omitempty"`
	MimeType       string `json:"mime_type,omitempty"`
	Size           int64  `json:"file_size,omitempty"`
	Blake3         string `json:"blake3_hash,omitempty"`
}

// Options configures a Store.
type Options struct {
	DataDir      string
	BlobDir      string
	MaxItemBytes int64
	Now          func() time.Time
}

// Store owns the SQLite database and the blob directory.
type Store struct {
	db           *sql.DB
	blobDir      string
	maxItemBytes int64
	now          func() time.Time

	saltOnce  sync.Once
	saltValue []byte
	saltErr   error
}

// Open creates the data and blob directories and migrates the schema.
func Open(options Options) (*Store, error) {
	if strings.TrimSpace(options.DataDir) == "" {
		return nil, errors.New("store: data directory is required")
	}
	dataDir, err := filepath.Abs(options.DataDir)
	if err != nil {
		return nil, fmt.Errorf("store: resolve data directory: %w", err)
	}
	blobDir := options.BlobDir
	if strings.TrimSpace(blobDir) == "" {
		blobDir = filepath.Join(dataDir, "blobs")
	}
	blobDir, err = filepath.Abs(blobDir)
	if err != nil {
		return nil, fmt.Errorf("store: resolve blob directory: %w", err)
	}
	if options.MaxItemBytes <= 0 {
		options.MaxItemBytes = DefaultMaxItemBytes
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}

	for _, dir := range []string{dataDir, blobDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("store: create directory %s: %w", dir, err)
		}
	}

	dbPath := filepath.Join(dataDir, "molisync.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("store: open database: %w", err)
	}
	// A single writer connection keeps SQLITE_BUSY impossible for this
	// workload and avoids surprises with WAL checkpointing.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{
		db:           db,
		blobDir:      blobDir,
		maxItemBytes: options.MaxItemBytes,
		now:          now,
	}

	pingContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingContext); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: ping database: %w", err)
	}

	for _, statement := range []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA synchronous = NORMAL`,
		`CREATE TABLE IF NOT EXISTS records (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL CHECK (kind IN ('text', 'image', 'file')),
			sender_device_id TEXT NOT NULL,
			sender_name TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			text_content TEXT NOT NULL DEFAULT '',
			blob_id TEXT NOT NULL DEFAULT '',
			file_name TEXT NOT NULL DEFAULT '',
			mime_type TEXT NOT NULL DEFAULT '',
			size INTEGER NOT NULL DEFAULT 0,
			blake3 TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_records_kind_created
			ON records (kind, created_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_records_created
			ON records (created_at DESC, id DESC)`,
		`CREATE TABLE IF NOT EXISTS devices (
			device_key TEXT PRIMARY KEY,
			platform TEXT NOT NULL,
			first_seen INTEGER NOT NULL,
			last_seen INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_devices_last_seen
			ON devices (last_seen DESC)`,
		`CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("store: migrate schema: %w", err)
		}
	}

	if _, err := db.Exec(
		`INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`,
		metaFirstSeen,
		strconv.FormatInt(now().UTC().Unix(), 10),
	); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: record first seen: %w", err)
	}

	return store, nil
}

// Close releases the database.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// MaxItemBytes is the maximum accepted blob size.
func (s *Store) MaxItemBytes() int64 {
	return s.maxItemBytes
}

// SaveText stores a text record.
func (s *Store) SaveText(ctx context.Context, senderID, senderName, text string) (Record, error) {
	if text == "" {
		return Record{}, errors.New("store: text must not be empty")
	}
	if len(text) > MaxTextBytes {
		return Record{}, ErrTooLarge
	}
	record := Record{
		RecordID:       newID(),
		SenderDeviceID: senderID,
		SenderName:     senderName,
		CreatedAt:      s.now().UnixMilli(),
		Kind:           KindText,
		Text:           text,
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO records
			(id, kind, sender_device_id, sender_name, created_at, text_content)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		record.RecordID, record.Kind, record.SenderDeviceID, record.SenderName,
		record.CreatedAt, record.Text,
	)
	if err != nil {
		return Record{}, fmt.Errorf("store: insert text record: %w", err)
	}
	return record, nil
}

// SaveBlob streams src into the blob directory and stores its metadata. The
// caller must pass -1 for expectedSize when the size is not known in advance.
func (s *Store) SaveBlob(
	ctx context.Context,
	kind, senderID, senderName, fileName, mimeType, blake3 string,
	expectedSize int64,
	src io.Reader,
) (Record, error) {
	if kind != KindImage && kind != KindFile {
		return Record{}, ErrInvalidKind
	}
	fileName = sanitizeFileName(fileName)
	if fileName == "" {
		fileName = "file"
	}
	if len(mimeType) > 255 {
		mimeType = mimeType[:255]
	}

	recordID := newID()
	blobID := recordID
	temporaryPath := filepath.Join(s.blobDir, blobID+".part")
	finalPath := filepath.Join(s.blobDir, blobID)

	file, err := os.OpenFile(temporaryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Record{}, fmt.Errorf("store: create blob: %w", err)
	}

	written, copyErr := io.Copy(file, io.LimitReader(src, s.maxItemBytes+1))
	if copyErr == nil && written > s.maxItemBytes {
		copyErr = ErrTooLarge
	}
	if copyErr == nil && written == 0 {
		copyErr = errors.New("store: blob must not be empty")
	}
	if copyErr == nil && expectedSize >= 0 && written != expectedSize {
		copyErr = fmt.Errorf("store: size mismatch: received %d bytes, expected %d", written, expectedSize)
	}
	if copyErr == nil {
		copyErr = file.Sync()
	}
	if closeErr := file.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(temporaryPath)
		return Record{}, copyErr
	}
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		_ = os.Remove(temporaryPath)
		return Record{}, fmt.Errorf("store: commit blob: %w", err)
	}

	record := Record{
		RecordID:       recordID,
		SenderDeviceID: senderID,
		SenderName:     senderName,
		CreatedAt:      s.now().UnixMilli(),
		Kind:           kind,
		BlobID:         blobID,
		FileName:       fileName,
		MimeType:       mimeType,
		Size:           written,
		Blake3:         strings.ToLower(blake3),
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO records
			(id, kind, sender_device_id, sender_name, created_at, blob_id,
			 file_name, mime_type, size, blake3)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.RecordID, record.Kind, record.SenderDeviceID, record.SenderName,
		record.CreatedAt, record.BlobID, record.FileName, record.MimeType,
		record.Size, record.Blake3,
	); err != nil {
		_ = os.Remove(finalPath)
		return Record{}, fmt.Errorf("store: insert blob record: %w", err)
	}
	return record, nil
}

// List returns records newest first. kind may be empty for all kinds.
func (s *Store) List(ctx context.Context, kind string, limit int, before int64) ([]Record, error) {
	if kind != "" && kind != KindText && kind != KindImage && kind != KindFile {
		return nil, ErrInvalidKind
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	query := `SELECT id, kind, sender_device_id, sender_name, created_at,
		text_content, blob_id, file_name, mime_type, size, blake3
		FROM records`
	var conditions []string
	var arguments []any
	if kind != "" {
		conditions = append(conditions, "kind = ?")
		arguments = append(arguments, kind)
	}
	if before > 0 {
		conditions = append(conditions, "created_at < ?")
		arguments = append(arguments, before)
	}
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ?"
	arguments = append(arguments, limit)

	rows, err := s.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("store: list records: %w", err)
	}
	defer rows.Close()

	records := make([]Record, 0, limit)
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list records: %w", err)
	}
	return records, nil
}

// Get returns one record by ID.
func (s *Store) Get(ctx context.Context, recordID string) (Record, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, kind, sender_device_id, sender_name, created_at,
			text_content, blob_id, file_name, mime_type, size, blake3
		 FROM records WHERE id = ?`, recordID)
	record, err := scanRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrNotFound
	}
	return record, err
}

// OpenBlob opens the blob file backing a record.
func (s *Store) OpenBlob(record Record) (*os.File, error) {
	if record.BlobID == "" {
		return nil, ErrNotFound
	}
	if !isHexID(record.BlobID) {
		return nil, ErrNotFound
	}
	file, err := os.Open(filepath.Join(s.blobDir, record.BlobID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: open blob: %w", err)
	}
	return file, nil
}

// Delete removes a record and its blob if the caller owns it.
func (s *Store) Delete(ctx context.Context, recordID, senderID string) error {
	record, err := s.Get(ctx, recordID)
	if err != nil {
		return err
	}
	if record.SenderDeviceID != senderID {
		return ErrForbidden
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM records WHERE id = ?`, recordID); err != nil {
		return fmt.Errorf("store: delete record: %w", err)
	}
	if record.BlobID != "" && isHexID(record.BlobID) {
		if err := os.Remove(filepath.Join(s.blobDir, record.BlobID)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("store: delete blob: %w", err)
		}
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanRecord(row rowScanner) (Record, error) {
	var record Record
	err := row.Scan(
		&record.RecordID, &record.Kind, &record.SenderDeviceID, &record.SenderName,
		&record.CreatedAt, &record.Text, &record.BlobID, &record.FileName,
		&record.MimeType, &record.Size, &record.Blake3,
	)
	if err != nil {
		return Record{}, err
	}
	return record, nil
}

func newID() string {
	var buffer [16]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		// crypto/rand only fails when the platform entropy source is broken;
		// refusing to serve is safer than duplicating identifiers.
		panic(fmt.Sprintf("store: cannot read random bytes: %v", err))
	}
	return hex.EncodeToString(buffer[:])
}

func isHexID(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func sanitizeFileName(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if index := strings.LastIndex(value, "/"); index >= 0 {
		value = value[index+1:]
	}
	cleaned := make([]rune, 0, len(value))
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			continue
		}
		cleaned = append(cleaned, character)
	}
	result := strings.TrimSpace(string(cleaned))
	if len(result) > 255 {
		result = result[:255]
	}
	if result == "" || result == "." || result == ".." {
		return ""
	}
	return result
}

package store

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Meta keys persisted in the `meta` table.
const (
	metaDeploymentID   = "deployment_id"
	metaDeploymentSalt = "deployment_salt"
	metaFirstSeen      = "first_seen"
)

// Platform buckets reported by the clients.
const (
	PlatformWindows = "windows"
	PlatformAndroid = "android"
	PlatformOther   = "other"
)

// DeviceStats aggregates the device history table.
type DeviceStats struct {
	Total     int
	Active7d  int
	Active30d int
	Windows   int
	Android   int
	Other     int
}

// RecordStats aggregates the record table.
type RecordStats struct {
	Text  int
	Image int
	File  int
	Bytes int64
}

// Snapshot is the complete local statistics view.
type Snapshot struct {
	Devices      DeviceStats
	Records      RecordStats
	FirstSeen    time.Time
	DeploymentID string
}

// DeploymentID returns the stable random identifier of this installation. It
// is generated on first use and never derived from any user data.
func (s *Store) DeploymentID(ctx context.Context) (string, error) {
	value, err := s.meta(ctx, metaDeploymentID)
	if err == nil {
		return value, nil
	}
	if !errors.Is(err, ErrMetaNotFound) {
		return "", err
	}
	generated, err := randomHex(16)
	if err != nil {
		return "", err
	}
	return s.metaOrCreate(ctx, metaDeploymentID, generated)
}

// FirstSeen returns the moment the database was created.
func (s *Store) FirstSeen(ctx context.Context) (time.Time, error) {
	value, err := s.meta(ctx, metaFirstSeen)
	if errors.Is(err, ErrMetaNotFound) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	seconds, err := parseUnix(value)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(seconds, 0).UTC(), nil
}

// TouchDevice records that a device connected and reports whether this is the
// first time it was ever seen, which is exactly the moment an installation is
// counted. deviceID is the raw certificate fingerprint; only a salted key is
// persisted and returned, so the database never stores a reversible identity.
func (s *Store) TouchDevice(ctx context.Context, deviceID, platform string) (deviceKey string, created bool, err error) {
	key, err := s.DeviceKey(deviceID)
	if err != nil {
		return "", false, err
	}
	now := s.now().UTC().Unix()
	// RETURNING yields a row only when the insert actually happened, which is
	// the reliable way to tell a first install from a reconnect: SQLite counts
	// both the insert and the upsert as one affected row.
	var insertedKey string
	err = s.db.QueryRowContext(ctx,
		`INSERT INTO devices (device_key, platform, first_seen, last_seen)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(device_key) DO NOTHING
		 RETURNING device_key`,
		key, NormalizePlatform(platform), now, now,
	).Scan(&insertedKey)
	if errors.Is(err, sql.ErrNoRows) {
		if _, updateErr := s.db.ExecContext(ctx,
			`UPDATE devices SET platform = ?, last_seen = ? WHERE device_key = ?`,
			NormalizePlatform(platform), now, key,
		); updateErr != nil {
			return key, false, fmt.Errorf("store: touch device: %w", updateErr)
		}
		return key, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: touch device: %w", err)
	}
	return key, true, nil
}

// DeviceKey derives the persisted pseudo-identifier for a device.
func (s *Store) DeviceKey(deviceID string) (string, error) {
	if strings.TrimSpace(deviceID) == "" {
		return "", errors.New("store: device id is required")
	}
	salt, err := s.salt()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, salt)
	mac.Write([]byte(deviceID))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// DeviceStats counts devices by age and platform.
func (s *Store) DeviceStats(ctx context.Context) (DeviceStats, error) {
	now := s.now().UTC()
	sevenDays := now.Add(-7 * 24 * time.Hour).Unix()
	thirtyDays := now.Add(-30 * 24 * time.Hour).Unix()

	var stats DeviceStats
	err := s.db.QueryRowContext(ctx,
		`SELECT
			COUNT(*),
			COALESCE(SUM(CASE WHEN last_seen >= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN last_seen >= ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN platform = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN platform = ? THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN platform NOT IN (?, ?) THEN 1 ELSE 0 END), 0)
		 FROM devices`,
		sevenDays, thirtyDays, PlatformWindows, PlatformAndroid, PlatformWindows, PlatformAndroid,
	).Scan(
		&stats.Total,
		&stats.Active7d,
		&stats.Active30d,
		&stats.Windows,
		&stats.Android,
		&stats.Other,
	)
	if err != nil {
		return DeviceStats{}, fmt.Errorf("store: count devices: %w", err)
	}
	return stats, nil
}

// RecordStats counts stored records and their total byte size.
func (s *Store) RecordStats(ctx context.Context) (RecordStats, error) {
	var stats RecordStats
	err := s.db.QueryRowContext(ctx,
		`SELECT
			COALESCE(SUM(CASE WHEN kind = 'text' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind = 'image' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN kind = 'file' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(size), 0)
		 FROM records`,
	).Scan(&stats.Text, &stats.Image, &stats.File, &stats.Bytes)
	if err != nil {
		return RecordStats{}, fmt.Errorf("store: count records: %w", err)
	}
	return stats, nil
}

// Snapshot collects everything the stats endpoint and the telemetry reporter
// need in one call.
func (s *Store) Snapshot(ctx context.Context) (Snapshot, error) {
	devices, err := s.DeviceStats(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	records, err := s.RecordStats(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	firstSeen, err := s.FirstSeen(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	deploymentID, err := s.DeploymentID(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Devices:      devices,
		Records:      records,
		FirstSeen:    firstSeen,
		DeploymentID: deploymentID,
	}, nil
}

// NormalizePlatform maps a client platform string into a reporting bucket.
func NormalizePlatform(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformWindows, "win", "win32", "windows-x64":
		return PlatformWindows
	case PlatformAndroid:
		return PlatformAndroid
	default:
		return PlatformOther
	}
}

// salt lazily creates and caches the deployment salt used for device keys.
func (s *Store) salt() ([]byte, error) {
	s.saltOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		value, err := s.meta(ctx, metaDeploymentSalt)
		if errors.Is(err, ErrMetaNotFound) {
			var generated []byte
			generated, err = randomBytes(32)
			if err != nil {
				s.saltErr = err
				return
			}
			value, err = s.metaOrCreate(ctx, metaDeploymentSalt, hex.EncodeToString(generated))
		}
		if err != nil {
			s.saltErr = err
			return
		}
		decoded, decodeErr := hex.DecodeString(value)
		if decodeErr != nil || len(decoded) != 32 {
			s.saltErr = errors.New("store: deployment salt is malformed")
			return
		}
		s.saltValue = decoded
	})
	return s.saltValue, s.saltErr
}

func (s *Store) meta(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrMetaNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: read meta %s: %w", key, err)
	}
	return value, nil
}

func (s *Store) metaOrCreate(ctx context.Context, key, value string) (string, error) {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO NOTHING`, key, value); err != nil {
		return "", fmt.Errorf("store: write meta %s: %w", key, err)
	}
	return s.meta(ctx, key)
}

func randomBytes(size int) ([]byte, error) {
	buffer := make([]byte, size)
	if _, err := rand.Read(buffer); err != nil {
		return nil, fmt.Errorf("store: read random bytes: %w", err)
	}
	return buffer, nil
}

func randomHex(size int) (string, error) {
	buffer, err := randomBytes(size)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func parseUnix(value string) (int64, error) {
	var seconds int64
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &seconds); err != nil {
		return 0, fmt.Errorf("store: invalid unix timestamp %q", value)
	}
	return seconds, nil
}

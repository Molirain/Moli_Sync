// Package collector stores the anonymous aggregate reports sent by MoliSync
// server deployments and answers global statistics queries.
package collector

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Schema is the report schema accepted by this collector.
const Schema = 1

// Report is one deployment snapshot. It intentionally mirrors only aggregate
// counters: no device identifier, name, address or content field exists.
type Report struct {
	Schema          int            `json:"schema"`
	DeploymentID    string         `json:"deployment_id"`
	ServerVersion   string         `json:"server_version"`
	ProtocolVersion int            `json:"protocol_version"`
	ReportedAt      int64          `json:"reported_at"`
	FirstSeen       int64          `json:"first_seen"`
	Devices         DeviceCounts   `json:"devices"`
	Platforms       PlatformCounts `json:"platforms"`
	Records         RecordCounts   `json:"records"`
}

// DeviceCounts is the device aggregate of one deployment.
type DeviceCounts struct {
	Total     int `json:"total"`
	Active7d  int `json:"active_7d"`
	Active30d int `json:"active_30d"`
	Online    int `json:"online"`
}

// PlatformCounts is the platform breakdown of one deployment.
type PlatformCounts struct {
	Windows int `json:"windows"`
	Android int `json:"android"`
	Other   int `json:"other"`
}

// RecordCounts is the stored record aggregate of one deployment.
type RecordCounts struct {
	Text  int   `json:"text"`
	Image int   `json:"image"`
	File  int   `json:"file"`
	Bytes int64 `json:"bytes"`
}

// Measure is the cross-deployment summary.
type Totals struct {
	GeneratedAt int64 `json:"generated_at"`
	// Installations counts every device that ever joined any deployment. It
	// only grows: a device is counted once, and deleting one never subtracts.
	Installations int            `json:"installations"`
	Deployments   int            `json:"deployments"`
	FirstSeen     int64          `json:"first_seen,omitempty"`
	NewLast7Days  int            `json:"new_installations_7d"`
	NewLast30Days int            `json:"new_installations_30d"`
	Devices       DeviceTotals   `json:"devices"`
	Platforms     PlatformCounts `json:"platforms"`
	Records       RecordCounts   `json:"records"`
	Versions      []VersionCount `json:"versions"`
}

// DeviceTotals is the device summary across deployments.
type DeviceTotals struct {
	Total     int `json:"total"`
	Active7d  int `json:"active_7d"`
	Active30d int `json:"active_30d"`
	Online    int `json:"online"`
}

// VersionCount reports how many deployments run a given server version.
type VersionCount struct {
	Version     string `json:"version"`
	Deployments int    `json:"deployments"`
	Devices     int    `json:"devices"`
}

// Store persists the latest report of every deployment.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open prepares the collector database.
func Open(dataDir string) (*Store, error) {
	if dataDir == "" {
		return nil, errors.New("collector: data directory is required")
	}
	absolute, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("collector: resolve data directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("collector: create data directory: %w", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(absolute, "collector.db"))
	if err != nil {
		return nil, fmt.Errorf("collector: open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	for _, statement := range []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA synchronous = NORMAL`,
		`CREATE TABLE IF NOT EXISTS deployments (
			deployment_id TEXT PRIMARY KEY,
			server_version TEXT NOT NULL DEFAULT '',
			first_seen INTEGER NOT NULL,
			reported_at INTEGER NOT NULL,
			devices_total INTEGER NOT NULL,
			active_7d INTEGER NOT NULL,
			active_30d INTEGER NOT NULL,
			online INTEGER NOT NULL,
			windows INTEGER NOT NULL,
			android INTEGER NOT NULL,
			other INTEGER NOT NULL,
			records_text INTEGER NOT NULL,
			records_image INTEGER NOT NULL,
			records_file INTEGER NOT NULL,
			records_bytes INTEGER NOT NULL
		)`,
		// One row per UTC day, holding how many devices first appeared that
		// day across all deployments. Only a number is stored: the collector
		// never learns which device it was.
		`CREATE TABLE IF NOT EXISTS install_events (
			day TEXT PRIMARY KEY,
			new_installations INTEGER NOT NULL
		)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("collector: migrate schema: %w", err)
		}
	}

	pingContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(pingContext); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("collector: ping database: %w", err)
	}

	return &Store{db: db, now: time.Now}, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// Record stores the latest report of a deployment. The device total is kept at
// its maximum, so removing a device never lowers the installation count, and
// the difference against the stored total is booked as a first-install event
// for the reporting day.
func (s *Store) Record(ctx context.Context, report Report) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("collector: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var previous int
	err = tx.QueryRowContext(ctx,
		`SELECT devices_total FROM deployments WHERE deployment_id = ?`,
		report.DeploymentID,
	).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("collector: read previous report: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO deployments (
			deployment_id, server_version, first_seen, reported_at,
			devices_total, active_7d, active_30d, online,
			windows, android, other,
			records_text, records_image, records_file, records_bytes
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(deployment_id) DO UPDATE SET
			server_version = excluded.server_version,
			reported_at = excluded.reported_at,
			devices_total = MAX(deployments.devices_total, excluded.devices_total),
			active_7d = excluded.active_7d,
			active_30d = excluded.active_30d,
			online = excluded.online,
			windows = excluded.windows,
			android = excluded.android,
			other = excluded.other,
			records_text = excluded.records_text,
			records_image = excluded.records_image,
			records_file = excluded.records_file,
			records_bytes = excluded.records_bytes`,
		report.DeploymentID,
		report.ServerVersion,
		report.FirstSeen,
		report.ReportedAt,
		report.Devices.Total,
		report.Devices.Active7d,
		report.Devices.Active30d,
		report.Devices.Online,
		report.Platforms.Windows,
		report.Platforms.Android,
		report.Platforms.Other,
		report.Records.Text,
		report.Records.Image,
		report.Records.File,
		report.Records.Bytes,
	); err != nil {
		return fmt.Errorf("collector: record report: %w", err)
	}

	if delta := report.Devices.Total - previous; delta > 0 {
		day := time.Unix(report.ReportedAt, 0).UTC().Format("2006-01-02")
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO install_events (day, new_installations) VALUES (?, ?)
			 ON CONFLICT(day) DO UPDATE SET
				new_installations = install_events.new_installations + excluded.new_installations`,
			day, delta,
		); err != nil {
			return fmt.Errorf("collector: record installations: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("collector: commit report: %w", err)
	}
	return nil
}

// Totals sums the latest report of every deployment.
func (s *Store) Totals(ctx context.Context) (Totals, error) {
	totals := Totals{GeneratedAt: s.now().UTC().Unix(), Versions: []VersionCount{}}

	err := s.db.QueryRowContext(ctx,
		`SELECT
			COUNT(*),
			COALESCE(MIN(CASE WHEN first_seen > 0 THEN first_seen END), 0),
			COALESCE(SUM(devices_total), 0),
			COALESCE(SUM(active_7d), 0),
			COALESCE(SUM(active_30d), 0),
			COALESCE(SUM(online), 0),
			COALESCE(SUM(windows), 0),
			COALESCE(SUM(android), 0),
			COALESCE(SUM(other), 0),
			COALESCE(SUM(records_text), 0),
			COALESCE(SUM(records_image), 0),
			COALESCE(SUM(records_file), 0),
			COALESCE(SUM(records_bytes), 0)
		 FROM deployments`,
	).Scan(
		&totals.Deployments,
		&totals.FirstSeen,
		&totals.Devices.Total,
		&totals.Devices.Active7d,
		&totals.Devices.Active30d,
		&totals.Devices.Online,
		&totals.Platforms.Windows,
		&totals.Platforms.Android,
		&totals.Platforms.Other,
		&totals.Records.Text,
		&totals.Records.Image,
		&totals.Records.File,
		&totals.Records.Bytes,
	)
	if err != nil {
		return Totals{}, fmt.Errorf("collector: sum totals: %w", err)
	}

	// Installations are the sum of every deployment's all-time device total;
	// the weekly and monthly figures come from the per-day deltas.
	totals.Installations = totals.Devices.Total

	now := s.now().UTC()
	if err := s.db.QueryRowContext(ctx,
		`SELECT
			COALESCE(SUM(CASE WHEN day >= ? THEN new_installations ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN day >= ? THEN new_installations ELSE 0 END), 0)
		 FROM install_events`,
		now.AddDate(0, 0, -7).Format("2006-01-02"),
		now.AddDate(0, 0, -30).Format("2006-01-02"),
	).Scan(&totals.NewLast7Days, &totals.NewLast30Days); err != nil {
		return Totals{}, fmt.Errorf("collector: sum installations: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT server_version, COUNT(*), COALESCE(SUM(devices_total), 0)
		 FROM deployments
		 GROUP BY server_version
		 ORDER BY COUNT(*) DESC, server_version DESC`)
	if err != nil {
		return Totals{}, fmt.Errorf("collector: group versions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var entry VersionCount
		if err := rows.Scan(&entry.Version, &entry.Deployments, &entry.Devices); err != nil {
			return Totals{}, fmt.Errorf("collector: scan version: %w", err)
		}
		totals.Versions = append(totals.Versions, entry)
	}
	if err := rows.Err(); err != nil {
		return Totals{}, fmt.Errorf("collector: group versions: %w", err)
	}

	return totals, nil
}

// Count returns the number of known deployments.
func (s *Store) Count(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM deployments`).Scan(&count); err != nil {
		return 0, fmt.Errorf("collector: count deployments: %w", err)
	}
	return count, nil
}

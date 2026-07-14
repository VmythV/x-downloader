package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"x-downloader/helper/internal/jobs"
	"x-downloader/helper/internal/media"
	"x-downloader/helper/internal/settings"
)

const legacyImportKey = "legacy_json_import_v1"

type legacyCandidates struct {
	Version    int               `json:"version"`
	Candidates []media.Candidate `json:"candidates"`
}

type legacyJobs struct {
	Version int              `json:"version"`
	Jobs    []jobs.StoredJob `json:"jobs"`
}

type legacySettings struct {
	Version int `json:"version"`
	settings.Values
}

func (database *Database) importLegacy(ctx context.Context, paths LegacyPaths) error {
	var imported string
	err := database.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key = ?", legacyImportKey).Scan(&imported)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check legacy JSON migration: %w", err)
	}

	var candidateState legacyCandidates
	candidatesFound, err := readLegacyJSON(paths.Candidates, &candidateState)
	if err != nil {
		return fmt.Errorf("read legacy candidates: %w", err)
	}
	var jobState legacyJobs
	jobsFound, err := readLegacyJSON(paths.Jobs, &jobState)
	if err != nil {
		return fmt.Errorf("read legacy jobs: %w", err)
	}
	var settingsState legacySettings
	settingsFound, err := readLegacyJSON(paths.Settings, &settingsState)
	if err != nil {
		return fmt.Errorf("read legacy settings: %w", err)
	}

	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	candidates := make(map[string]media.Candidate)
	for _, candidate := range candidateState.Candidates {
		candidates[candidate.ID] = candidate
	}
	for _, item := range jobState.Jobs {
		if item.Candidate.ID != "" {
			candidates[item.Candidate.ID] = item.Candidate
		}
	}
	for _, candidate := range candidates {
		if err := upsertCandidateTx(ctx, tx, candidate, candidate.DiscoveredAt); err != nil {
			return fmt.Errorf("import legacy candidate %s: %w", candidate.ID, err)
		}
	}
	if err := upsertJobsTx(ctx, tx, jobState.Jobs); err != nil {
		return fmt.Errorf("import legacy jobs: %w", err)
	}
	if settingsFound {
		values := paths.Defaults
		if settingsState.DownloadDir != "" {
			values.DownloadDir = settingsState.DownloadDir
		}
		if settingsState.Version >= 2 {
			values.FilenameTemplate = settingsState.FilenameTemplate
			values.Concurrency = settingsState.Concurrency
			values.RetryCount = settingsState.RetryCount
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO app_settings(id, download_dir, filename_template, concurrency, retry_count, updated_at)
VALUES (1, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  download_dir = excluded.download_dir,
  filename_template = excluded.filename_template,
  concurrency = excluded.concurrency,
  retry_count = excluded.retry_count,
  updated_at = excluded.updated_at`, values.DownloadDir, values.FilenameTemplate,
			values.Concurrency, values.RetryCount, time.Now().UTC().UnixMilli()); err != nil {
			return fmt.Errorf("import legacy settings: %w", err)
		}
	}

	summary := fmt.Sprintf("candidates=%d,jobs=%d,settings=%t", len(candidates), len(jobState.Jobs), settingsFound)
	if _, err := tx.ExecContext(ctx, "INSERT INTO metadata(key, value) VALUES (?, ?)", legacyImportKey, summary); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy JSON migration: %w", err)
	}
	var integrity string
	if err := database.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil {
		return fmt.Errorf("verify migrated SQLite database: %w", err)
	}
	if integrity != "ok" {
		return fmt.Errorf("verify migrated SQLite database: %s", integrity)
	}

	for path, found := range map[string]bool{
		paths.Candidates: candidatesFound,
		paths.Jobs:       jobsFound,
		paths.Settings:   settingsFound,
	} {
		if found {
			if err := preserveLegacyBackup(path); err != nil {
				slog.Warn("preserve migrated JSON backup", "path", path, "error", err)
			}
		}
	}
	return nil
}

func readLegacyJSON(path string, destination any) (bool, error) {
	if path == "" {
		return false, nil
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<20))
	if err := decoder.Decode(destination); err != nil {
		return false, err
	}
	return true, nil
}

func preserveLegacyBackup(path string) error {
	backupPath := path + ".migrated-v1.bak"
	if _, err := os.Stat(backupPath); err == nil {
		return os.Remove(path)
	}
	if err := os.Rename(path, backupPath); err != nil {
		return fmt.Errorf("preserve %s: %w", filepath.Base(path), err)
	}
	return os.Chmod(backupPath, 0o600)
}

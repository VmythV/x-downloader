package storage

import (
	"database/sql"
	"errors"
	"time"

	"x-downloader/helper/internal/settings"
)

func (database *Database) LoadSettings() (settings.Values, bool, error) {
	var values settings.Values
	err := database.db.QueryRow(`
SELECT download_dir, filename_template, concurrency, retry_count
FROM app_settings WHERE id = 1`).Scan(
		&values.DownloadDir, &values.FilenameTemplate, &values.Concurrency, &values.RetryCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return settings.Values{}, false, nil
	}
	if err != nil {
		return settings.Values{}, false, err
	}
	return values, true, nil
}

func (database *Database) SaveSettings(values settings.Values) error {
	_, err := database.db.Exec(`
INSERT INTO app_settings(
  id, download_dir, filename_template, concurrency, retry_count, updated_at
) VALUES (1, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  download_dir = excluded.download_dir,
  filename_template = excluded.filename_template,
  concurrency = excluded.concurrency,
  retry_count = excluded.retry_count,
  updated_at = excluded.updated_at`,
		values.DownloadDir, values.FilenameTemplate, values.Concurrency, values.RetryCount,
		time.Now().UTC().UnixMilli(),
	)
	return err
}

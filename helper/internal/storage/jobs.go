package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"x-downloader/helper/internal/hls"
	"x-downloader/helper/internal/jobs"
)

func (database *Database) UpsertJobs(items []jobs.StoredJob) error {
	if len(items) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertJobsTx(ctx, tx, items); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertJobRecordTx(ctx context.Context, tx *sql.Tx, item jobs.StoredJob) error {
	job := item.Job
	if job.ID == "" || job.CandidateID == "" {
		return errors.New("job ID and candidate ID are required")
	}
	var candidateExists int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM candidates WHERE candidate_id = ?", job.CandidateID).Scan(&candidateExists)
	if errors.Is(err, sql.ErrNoRows) {
		if err := upsertCandidateTx(ctx, tx, item.Candidate, time.Now().UTC()); err != nil {
			return fmt.Errorf("persist job candidate: %w", err)
		}
	} else if err != nil {
		return err
	}

	fileSize := int64(0)
	if job.Status == "completed" && job.OutputPath != "" {
		if info, err := os.Stat(job.OutputPath); err == nil {
			fileSize = info.Size()
		}
	}
	audioURL := ""
	audioBitrate := 0
	if item.Variant.Audio != nil {
		audioURL = item.Variant.Audio.URL
		audioBitrate = item.Variant.Audio.Bitrate
	}
	errorCode := classifyError(job.Status, job.Error)
	_, err = tx.ExecContext(ctx, `
INSERT INTO download_jobs(
  id, candidate_id, variant_id, media_id, width, height, video_url, audio_url,
  audio_bitrate, status, out_time_seconds, duration_seconds, percent, speed, phase,
  output_path, temp_path, error, error_code, attempt, max_attempts, file_size,
  created_at, started_at, finished_at, revision
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  candidate_id = excluded.candidate_id,
  variant_id = excluded.variant_id,
  media_id = excluded.media_id,
  width = excluded.width,
  height = excluded.height,
  video_url = excluded.video_url,
  audio_url = excluded.audio_url,
  audio_bitrate = excluded.audio_bitrate,
  status = excluded.status,
  out_time_seconds = excluded.out_time_seconds,
  duration_seconds = excluded.duration_seconds,
  percent = excluded.percent,
  speed = excluded.speed,
  phase = excluded.phase,
  output_path = excluded.output_path,
  temp_path = excluded.temp_path,
  error = excluded.error,
  error_code = excluded.error_code,
  attempt = excluded.attempt,
  max_attempts = excluded.max_attempts,
  file_size = MAX(download_jobs.file_size, excluded.file_size),
  started_at = excluded.started_at,
  finished_at = excluded.finished_at,
  revision = excluded.revision`,
		job.ID, job.CandidateID, job.VariantID, job.MediaID, job.Width, job.Height,
		item.Variant.URL, audioURL, audioBitrate, job.Status,
		job.Progress.OutTimeSeconds, job.Progress.DurationSeconds, job.Progress.Percent,
		job.Progress.Speed, job.Progress.Phase, job.OutputPath, item.TempPath,
		job.Error, errorCode, job.Attempt, job.MaxAttempts, fileSize,
		milliseconds(job.CreatedAt), nullableMilliseconds(job.StartedAt),
		nullableMilliseconds(job.FinishedAt), job.Revision,
	)
	if err != nil {
		return fmt.Errorf("upsert download job: %w", err)
	}

	if job.Attempt > 0 {
		startedAt := milliseconds(time.Now().UTC())
		if job.StartedAt != nil {
			startedAt = milliseconds(*job.StartedAt)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO job_attempts(job_id, attempt_no, started_at)
VALUES (?, ?, ?)
ON CONFLICT(job_id, attempt_no) DO NOTHING`, job.ID, job.Attempt, startedAt); err != nil {
			return fmt.Errorf("start job attempt: %w", err)
		}
		outcome := ""
		finishedAt := any(nil)
		if job.Status == "queued" && job.Error != "" {
			outcome = "retrying"
			finishedAt = time.Now().UTC().UnixMilli()
		} else if job.Status == "completed" || job.Status == "failed" || job.Status == "cancelled" {
			outcome = job.Status
			finishedAt = nullableMilliseconds(job.FinishedAt)
		}
		if outcome != "" {
			if _, err := tx.ExecContext(ctx, `
UPDATE job_attempts
SET finished_at = ?, outcome = ?, error_code = ?, error = ?
WHERE job_id = ? AND attempt_no = ?`,
				finishedAt, outcome, errorCode, job.Error, job.ID, job.Attempt,
			); err != nil {
				return fmt.Errorf("finish job attempt: %w", err)
			}
		}
	}

	return nil
}

func upsertJobsTx(ctx context.Context, tx *sql.Tx, items []jobs.StoredJob) error {
	candidateIDs := make(map[string]struct{}, len(items))
	for _, item := range items {
		if err := upsertJobRecordTx(ctx, tx, item); err != nil {
			return err
		}
		candidateIDs[item.Job.CandidateID] = struct{}{}
	}
	libraryItemIDs := make(map[int64]struct{}, len(candidateIDs))
	for candidateID := range candidateIDs {
		var libraryItemID int64
		if err := tx.QueryRowContext(ctx, `
SELECT library_item_id FROM media_items WHERE candidate_id = ?`, candidateID).Scan(&libraryItemID); err != nil {
			return err
		}
		libraryItemIDs[libraryItemID] = struct{}{}
	}
	for libraryItemID := range libraryItemIDs {
		if err := refreshSearchDocumentTx(ctx, tx, libraryItemID); err != nil {
			return err
		}
	}
	return nil
}

func (database *Database) LoadJobs(recentTerminalLimit int) ([]jobs.StoredJob, error) {
	if recentTerminalLimit <= 0 || recentTerminalLimit > 5000 {
		recentTerminalLimit = 500
	}
	rows, err := database.db.Query(`
SELECT id, candidate_id, variant_id, media_id, width, height, video_url, audio_url,
       audio_bitrate, status, out_time_seconds, duration_seconds, percent, speed,
       phase, output_path, temp_path, error, attempt, max_attempts, created_at,
       started_at, finished_at, revision
FROM download_jobs
WHERE status IN ('queued', 'downloading')
   OR id IN (
     SELECT id FROM download_jobs
     WHERE status NOT IN ('queued', 'downloading')
     ORDER BY created_at DESC, id DESC LIMIT ?
   )
ORDER BY created_at DESC, id DESC`, recentTerminalLimit)
	if err != nil {
		return nil, err
	}
	items, err := scanStoredJobs(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	for index := range items {
		candidate, err := database.GetCandidate(items[index].Job.CandidateID)
		if err != nil {
			return nil, err
		}
		items[index].Candidate = candidate
		items[index].Variant = matchingVariant(candidate.Variants, items[index].Variant)
	}
	return items, nil
}

func (database *Database) GetStoredJob(id string) (jobs.StoredJob, error) {
	rows, err := database.db.Query(`
SELECT id, candidate_id, variant_id, media_id, width, height, video_url, audio_url,
       audio_bitrate, status, out_time_seconds, duration_seconds, percent, speed,
       phase, output_path, temp_path, error, attempt, max_attempts, created_at,
       started_at, finished_at, revision
FROM download_jobs WHERE id = ?`, id)
	if err != nil {
		return jobs.StoredJob{}, err
	}
	items, scanErr := scanStoredJobs(rows)
	rows.Close()
	if scanErr != nil {
		return jobs.StoredJob{}, scanErr
	}
	if len(items) == 0 {
		return jobs.StoredJob{}, jobs.ErrJobNotFound
	}
	item := items[0]
	candidate, err := database.GetCandidate(item.Job.CandidateID)
	if err != nil {
		return jobs.StoredJob{}, err
	}
	item.Candidate = candidate
	item.Variant = matchingVariant(candidate.Variants, item.Variant)
	return item, nil
}

func scanStoredJobs(rows *sql.Rows) ([]jobs.StoredJob, error) {
	defer rows.Close()
	result := make([]jobs.StoredJob, 0)
	for rows.Next() {
		var item jobs.StoredJob
		var audioURL string
		var audioBitrate int
		var createdAt int64
		var startedAt, finishedAt sql.NullInt64
		if err := rows.Scan(
			&item.Job.ID, &item.Job.CandidateID, &item.Job.VariantID, &item.Job.MediaID,
			&item.Job.Width, &item.Job.Height, &item.Variant.URL, &audioURL, &audioBitrate,
			&item.Job.Status, &item.Job.Progress.OutTimeSeconds, &item.Job.Progress.DurationSeconds,
			&item.Job.Progress.Percent, &item.Job.Progress.Speed, &item.Job.Progress.Phase,
			&item.Job.OutputPath, &item.TempPath, &item.Job.Error, &item.Job.Attempt,
			&item.Job.MaxAttempts, &createdAt, &startedAt, &finishedAt, &item.Job.Revision,
		); err != nil {
			return nil, err
		}
		item.Job.CreatedAt = timeFromMilliseconds(createdAt)
		if startedAt.Valid {
			value := timeFromMilliseconds(startedAt.Int64)
			item.Job.StartedAt = &value
		}
		if finishedAt.Valid {
			value := timeFromMilliseconds(finishedAt.Int64)
			item.Job.FinishedAt = &value
		}
		item.Variant.ID = item.Job.VariantID
		item.Variant.Width = item.Job.Width
		item.Variant.Height = item.Job.Height
		if audioURL != "" {
			item.Variant.Audio = &hls.Audio{URL: audioURL, Bitrate: audioBitrate}
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func matchingVariant(variants []hls.Variant, fallback hls.Variant) hls.Variant {
	for _, variant := range variants {
		if variant.ID == fallback.ID {
			return variant
		}
	}
	return fallback
}

func (database *Database) JobStats() (map[string]int, error) {
	rows, err := database.db.Query("SELECT status, COUNT(*) FROM download_jobs GROUP BY status")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]int{"total": 0}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		result[status] = count
		result["total"] += count
	}
	return result, rows.Err()
}

func classifyError(status, message string) string {
	if message == "" {
		if status == "cancelled" {
			return "cancelled"
		}
		return ""
	}
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "interrupted because helper restarted"):
		return "helper_interrupted"
	case strings.Contains(lower, "cross-device") || strings.Contains(lower, "move completed file") || strings.Contains(lower, "finaliz"):
		return "finalization_failed"
	case strings.Contains(lower, "ffmpeg"):
		return "ffmpeg_failed"
	case strings.Contains(lower, "expired") || strings.Contains(lower, "http 403") || strings.Contains(lower, "http 404"):
		return "playlist_expired"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "network") || strings.Contains(lower, "connection"):
		return "network"
	default:
		return "unknown"
	}
}

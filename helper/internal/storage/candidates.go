package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"x-downloader/helper/internal/hls"
	"x-downloader/helper/internal/media"
)

func (database *Database) UpsertCandidates(candidates []media.Candidate) error {
	if len(candidates) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	observedAt := time.Now().UTC()
	for _, candidate := range candidates {
		if err := upsertCandidateTx(ctx, tx, candidate, observedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func upsertCandidateTx(ctx context.Context, tx *sql.Tx, candidate media.Candidate, observedAt time.Time) error {
	if candidate.ID == "" || candidate.MediaID == "" {
		return errors.New("candidate ID and media ID are required")
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	firstSeenAt := candidate.DiscoveredAt
	if firstSeenAt.IsZero() {
		firstSeenAt = observedAt
	}
	sourceKey := "media:" + candidate.MediaID
	if candidate.Context.PostID != "" {
		sourceKey = "post:" + candidate.Context.PostID
	}

	var libraryItemID int64
	err := tx.QueryRowContext(ctx, `
INSERT INTO library_items(
  source_key, post_id, author, post_url, page_url, post_created_at,
  first_seen_at, last_seen_at
) VALUES (?, ?, ?, ?, ?, NULLIF(?, 0), ?, ?)
ON CONFLICT(source_key) DO UPDATE SET
  post_id = CASE WHEN excluded.post_id <> '' THEN excluded.post_id ELSE library_items.post_id END,
  author = CASE WHEN excluded.author <> '' THEN excluded.author ELSE library_items.author END,
  post_url = CASE WHEN excluded.post_url <> '' THEN excluded.post_url ELSE library_items.post_url END,
  page_url = CASE WHEN excluded.page_url <> '' THEN excluded.page_url ELSE library_items.page_url END,
  post_created_at = COALESCE(excluded.post_created_at, library_items.post_created_at),
  last_seen_at = MAX(library_items.last_seen_at, excluded.last_seen_at)
RETURNING id`,
		sourceKey, candidate.Context.PostID, candidate.Context.Author,
		candidate.Context.PostURL, candidate.Context.PageURL, milliseconds(candidate.Context.CreatedAt),
		milliseconds(firstSeenAt), milliseconds(observedAt),
	).Scan(&libraryItemID)
	if err != nil {
		return fmt.Errorf("upsert library item: %w", err)
	}

	var previousLibraryItemID sql.NullInt64
	if err := tx.QueryRowContext(ctx, `
SELECT library_item_id FROM media_items WHERE candidate_id = ?`, candidate.ID).Scan(&previousLibraryItemID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read previous media history: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO media_items(
  library_item_id, candidate_id, media_id, media_index, thumbnail_url,
  first_seen_at, last_seen_at, seen_count
) VALUES (?, ?, ?, ?, ?, ?, ?, 1)
ON CONFLICT(candidate_id) DO UPDATE SET
  library_item_id = excluded.library_item_id,
  media_id = excluded.media_id,
  media_index = excluded.media_index,
  thumbnail_url = CASE WHEN excluded.thumbnail_url <> '' THEN excluded.thumbnail_url ELSE media_items.thumbnail_url END,
  last_seen_at = MAX(media_items.last_seen_at, excluded.last_seen_at),
  seen_count = media_items.seen_count + 1`,
		libraryItemID, candidate.ID, candidate.MediaID, candidate.Context.MediaIndex,
		candidate.Context.ThumbnailURL, milliseconds(firstSeenAt), milliseconds(observedAt),
	); err != nil {
		return fmt.Errorf("upsert media item: %w", err)
	}
	if previousLibraryItemID.Valid && previousLibraryItemID.Int64 != libraryItemID {
		if _, err := tx.ExecContext(ctx, `
UPDATE library_items
SET note = CASE WHEN note = '' THEN COALESCE((SELECT note FROM library_items WHERE id = ?), '') ELSE note END
WHERE id = ?`, previousLibraryItemID.Int64, libraryItemID); err != nil {
			return fmt.Errorf("merge media history note: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO library_item_tags(library_item_id, tag_id, created_at)
SELECT ?, tag_id, created_at FROM library_item_tags WHERE library_item_id = ?
ON CONFLICT(library_item_id, tag_id) DO NOTHING`, libraryItemID, previousLibraryItemID.Int64); err != nil {
			return fmt.Errorf("merge media history tags: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
DELETE FROM library_items
WHERE id = ? AND NOT EXISTS (SELECT 1 FROM media_items WHERE library_item_id = ?)`,
			previousLibraryItemID.Int64, previousLibraryItemID.Int64); err != nil {
			return fmt.Errorf("remove empty media history: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO candidates(candidate_id, master_url, user_agent, discovered_at, last_seen_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(candidate_id) DO UPDATE SET
  master_url = excluded.master_url,
  user_agent = excluded.user_agent,
  discovered_at = MIN(candidates.discovered_at, excluded.discovered_at),
  last_seen_at = MAX(candidates.last_seen_at, excluded.last_seen_at)`,
		candidate.ID, candidate.MasterURL, candidate.UserAgent, milliseconds(firstSeenAt), milliseconds(observedAt),
	); err != nil {
		return fmt.Errorf("upsert candidate: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM media_variants WHERE candidate_id = ?", candidate.ID); err != nil {
		return fmt.Errorf("replace candidate variants: %w", err)
	}
	for _, variant := range candidate.Variants {
		audio := hls.Audio{}
		if variant.Audio != nil {
			audio = *variant.Audio
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO media_variants(
  candidate_id, variant_id, video_url, width, height, bandwidth, average_bandwidth,
  codecs, audio_group, audio_name, audio_url, audio_bitrate
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			candidate.ID, variant.ID, variant.URL, variant.Width, variant.Height,
			variant.Bandwidth, variant.AverageBandwidth, variant.Codecs, variant.AudioGroup,
			audio.Name, audio.URL, audio.Bitrate,
		); err != nil {
			return fmt.Errorf("insert candidate variant: %w", err)
		}
	}
	return refreshSearchDocumentTx(ctx, tx, libraryItemID)
}

func (database *Database) LoadRecentCandidates(limit int) ([]media.Candidate, error) {
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	rows, err := database.db.Query(`
SELECT c.candidate_id
FROM candidates c
ORDER BY c.last_seen_at DESC, c.candidate_id DESC
LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := make([]media.Candidate, 0, len(ids))
	for _, id := range ids {
		candidate, err := database.GetCandidate(id)
		if err != nil {
			return nil, err
		}
		result = append(result, candidate)
	}
	return result, nil
}

func (database *Database) GetCandidate(id string) (media.Candidate, error) {
	row := database.db.QueryRow(`
SELECT c.candidate_id, mi.media_id, c.master_url, c.user_agent,
       li.page_url, li.post_url, li.post_id, li.author, li.post_created_at,
       mi.media_index, mi.thumbnail_url, c.discovered_at
FROM candidates c
JOIN media_items mi ON mi.candidate_id = c.candidate_id
JOIN library_items li ON li.id = mi.library_item_id
WHERE c.candidate_id = ?`, id)
	var candidate media.Candidate
	var postCreated sql.NullInt64
	var discovered int64
	if err := row.Scan(
		&candidate.ID, &candidate.MediaID, &candidate.MasterURL, &candidate.UserAgent,
		&candidate.Context.PageURL, &candidate.Context.PostURL, &candidate.Context.PostID,
		&candidate.Context.Author, &postCreated, &candidate.Context.MediaIndex,
		&candidate.Context.ThumbnailURL, &discovered,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return media.Candidate{}, media.ErrCandidateNotFound
		}
		return media.Candidate{}, err
	}
	candidate.Context.CreatedAt = timeFromMilliseconds(int64Value(postCreated))
	candidate.DiscoveredAt = timeFromMilliseconds(discovered)

	rows, err := database.db.Query(`
SELECT variant_id, video_url, width, height, bandwidth, average_bandwidth,
       codecs, audio_group, audio_name, audio_url, audio_bitrate
FROM media_variants
WHERE candidate_id = ?
ORDER BY height DESC, width DESC, bandwidth DESC`, id)
	if err != nil {
		return media.Candidate{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var variant hls.Variant
		var audio hls.Audio
		if err := rows.Scan(
			&variant.ID, &variant.URL, &variant.Width, &variant.Height, &variant.Bandwidth,
			&variant.AverageBandwidth, &variant.Codecs, &variant.AudioGroup,
			&audio.Name, &audio.URL, &audio.Bitrate,
		); err != nil {
			return media.Candidate{}, err
		}
		if audio.URL != "" {
			audio.GroupID = variant.AudioGroup
			variant.Audio = &audio
		}
		candidate.Variants = append(candidate.Variants, variant)
	}
	if err := rows.Err(); err != nil {
		return media.Candidate{}, err
	}
	return candidate, nil
}

func (database *Database) CandidateCount() (int, error) {
	var count int
	err := database.db.QueryRow("SELECT COUNT(*) FROM candidates").Scan(&count)
	return count, err
}

func refreshSearchDocumentTx(ctx context.Context, tx *sql.Tx, libraryItemID int64) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO history_search_documents(library_item_id, author, post_id, note, filenames)
SELECT li.id, li.author, li.post_id, li.note,
       COALESCE((
         SELECT GROUP_CONCAT(dj.output_path, ' ')
         FROM download_jobs dj
         JOIN media_items mi ON mi.candidate_id = dj.candidate_id
         WHERE mi.library_item_id = li.id
       ), '')
FROM library_items li
WHERE li.id = ?
ON CONFLICT(library_item_id) DO UPDATE SET
  author = excluded.author,
  post_id = excluded.post_id,
  note = excluded.note,
  filenames = excluded.filenames`, libraryItemID)
	return err
}

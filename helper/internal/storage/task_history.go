package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"x-downloader/helper/internal/jobs"
)

type JobQuery struct {
	Query  string
	Status string
	Cursor string
	Limit  int
}

type JobPage struct {
	Items      []jobs.Job `json:"items"`
	NextCursor string     `json:"nextCursor,omitempty"`
	HasMore    bool       `json:"hasMore"`
}

func (database *Database) SearchJobs(ctx context.Context, query JobQuery) (JobPage, error) {
	if query.Limit <= 0 {
		query.Limit = 100
	}
	if query.Limit > 100 {
		query.Limit = 100
	}
	where := []string{"1 = 1"}
	args := make([]any, 0, 8)
	if query.Status != "" {
		where = append(where, "status = ?")
		args = append(args, query.Status)
	}
	if search := strings.TrimSpace(query.Query); search != "" {
		pattern := "%" + escapeLike(search) + "%"
		where = append(where, `(output_path LIKE ? ESCAPE '\' OR media_id LIKE ? ESCAPE '\' OR candidate_id LIKE ? ESCAPE '\')`)
		args = append(args, pattern, pattern, pattern)
	}
	if createdAt, id, ok := decodeJobCursor(query.Cursor); ok {
		where = append(where, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, createdAt, createdAt, id)
	}
	args = append(args, query.Limit+1)
	rows, err := database.db.QueryContext(ctx, `
SELECT id, candidate_id, variant_id, media_id, width, height, video_url, audio_url,
       audio_bitrate, status, out_time_seconds, duration_seconds, percent, speed,
       phase, output_path, temp_path, error, attempt, max_attempts, created_at,
       started_at, finished_at, revision
FROM download_jobs
WHERE `+strings.Join(where, " AND ")+`
ORDER BY created_at DESC, id DESC
LIMIT ?`, args...)
	if err != nil {
		return JobPage{}, err
	}
	stored, err := scanStoredJobs(rows)
	if err != nil {
		return JobPage{}, err
	}
	hasMore := len(stored) > query.Limit
	if hasMore {
		stored = stored[:query.Limit]
	}
	items := make([]jobs.Job, 0, len(stored))
	for _, item := range stored {
		items = append(items, item.Job)
	}
	page := JobPage{Items: items, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		page.NextCursor = encodeJobCursor(last.CreatedAt.UnixMilli(), last.ID)
	}
	return page, nil
}

func ParseJobQuery(values url.Values) (JobQuery, error) {
	query := JobQuery{
		Query: first(values["query"]), Status: first(values["status"]), Cursor: first(values["cursor"]),
	}
	if query.Status != "" && !map[string]bool{
		"queued": true, "downloading": true, "completed": true, "failed": true, "cancelled": true,
	}[query.Status] {
		return JobQuery{}, errors.New("unknown job status")
	}
	if query.Cursor != "" {
		if _, _, ok := decodeJobCursor(query.Cursor); !ok {
			return JobQuery{}, errors.New("invalid job cursor")
		}
	}
	if value := first(values["limit"]); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil {
			return JobQuery{}, errors.New("limit must be an integer")
		}
		query.Limit = limit
	}
	return query, nil
}

func encodeJobCursor(createdAt int64, id string) string {
	payload, _ := json.Marshal(struct {
		CreatedAt int64  `json:"createdAt"`
		ID        string `json:"id"`
	}{createdAt, id})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeJobCursor(value string) (int64, string, bool) {
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, "", false
	}
	var cursor struct {
		CreatedAt int64  `json:"createdAt"`
		ID        string `json:"id"`
	}
	if json.Unmarshal(payload, &cursor) != nil || cursor.CreatedAt <= 0 || cursor.ID == "" {
		return 0, "", false
	}
	return cursor.CreatedAt, cursor.ID, true
}

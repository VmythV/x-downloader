package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type HistoryQuery struct {
	Query  string
	Status string
	TagID  int64
	From   time.Time
	To     time.Time
	Cursor string
	Limit  int
}

type Tag struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type HistoryItem struct {
	ID            int64      `json:"id"`
	PostID        string     `json:"postId,omitempty"`
	Author        string     `json:"author,omitempty"`
	PostURL       string     `json:"postUrl,omitempty"`
	PageURL       string     `json:"pageUrl,omitempty"`
	PostCreatedAt *time.Time `json:"postCreatedAt,omitempty"`
	Note          string     `json:"note,omitempty"`
	FirstSeenAt   time.Time  `json:"firstSeenAt"`
	LastSeenAt    time.Time  `json:"lastSeenAt"`
	MediaCount    int        `json:"mediaCount"`
	JobCount      int        `json:"jobCount"`
	Completed     int        `json:"completed"`
	Failed        int        `json:"failed"`
	LatestStatus  string     `json:"latestStatus,omitempty"`
	LatestJobAt   *time.Time `json:"latestJobAt,omitempty"`
	Tags          []Tag      `json:"tags"`
}

type HistoryPage struct {
	Items      []HistoryItem `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
	HasMore    bool          `json:"hasMore"`
}

func (database *Database) SearchHistory(ctx context.Context, query HistoryQuery) (HistoryPage, error) {
	if query.Limit <= 0 {
		query.Limit = 50
	}
	if query.Limit > 100 {
		query.Limit = 100
	}

	where := []string{"1 = 1"}
	args := make([]any, 0)
	search := strings.TrimSpace(query.Query)
	if search != "" {
		if utf8.RuneCountInString(search) >= 3 {
			where = append(where, "li.id IN (SELECT rowid FROM history_fts WHERE history_fts MATCH ?)")
			args = append(args, quoteFTSQuery(search))
		} else {
			where = append(where, `(
  li.author LIKE ? ESCAPE '\' OR li.post_id LIKE ? ESCAPE '\' OR
  li.note LIKE ? ESCAPE '\' OR EXISTS (
    SELECT 1 FROM history_search_documents sd
    WHERE sd.library_item_id = li.id AND sd.filenames LIKE ? ESCAPE '\'
  )
)`)
			pattern := "%" + escapeLike(search) + "%"
			args = append(args, pattern, pattern, pattern, pattern)
		}
	}
	if query.Status != "" {
		where = append(where, `EXISTS (
  SELECT 1 FROM media_items fmi
  JOIN download_jobs fdj ON fdj.candidate_id = fmi.candidate_id
  WHERE fmi.library_item_id = li.id AND fdj.status = ?
)`)
		args = append(args, query.Status)
	}
	if query.TagID > 0 {
		where = append(where, `EXISTS (
  SELECT 1 FROM library_item_tags lit
  WHERE lit.library_item_id = li.id AND lit.tag_id = ?
)`)
		args = append(args, query.TagID)
	}
	if !query.From.IsZero() {
		where = append(where, "li.last_seen_at >= ?")
		args = append(args, milliseconds(query.From))
	}
	if !query.To.IsZero() {
		where = append(where, "li.last_seen_at < ?")
		args = append(args, milliseconds(query.To))
	}
	if cursorTime, cursorID, ok := decodeHistoryCursor(query.Cursor); ok {
		where = append(where, "(li.last_seen_at < ? OR (li.last_seen_at = ? AND li.id < ?))")
		args = append(args, cursorTime, cursorTime, cursorID)
	}

	statement := `
SELECT li.id, li.post_id, li.author, li.post_url, li.page_url, li.post_created_at,
       li.note, li.first_seen_at, li.last_seen_at,
       COUNT(DISTINCT mi.id) AS media_count,
       COUNT(DISTINCT dj.id) AS job_count,
       COUNT(DISTINCT CASE WHEN dj.status = 'completed' THEN dj.id END) AS completed,
       COUNT(DISTINCT CASE WHEN dj.status = 'failed' THEN dj.id END) AS failed,
       COALESCE((
         SELECT dj2.status FROM download_jobs dj2
         JOIN media_items mi2 ON mi2.candidate_id = dj2.candidate_id
         WHERE mi2.library_item_id = li.id
         ORDER BY dj2.created_at DESC, dj2.id DESC LIMIT 1
       ), '') AS latest_status,
       MAX(dj.created_at) AS latest_job_at
FROM library_items li
LEFT JOIN media_items mi ON mi.library_item_id = li.id
LEFT JOIN download_jobs dj ON dj.candidate_id = mi.candidate_id
WHERE ` + strings.Join(where, " AND ") + `
GROUP BY li.id
ORDER BY li.last_seen_at DESC, li.id DESC
LIMIT ?`
	args = append(args, query.Limit+1)
	rows, err := database.db.QueryContext(ctx, statement, args...)
	if err != nil {
		return HistoryPage{}, fmt.Errorf("search history: %w", err)
	}
	items := make([]HistoryItem, 0, query.Limit+1)
	for rows.Next() {
		var item HistoryItem
		var postCreated, latestJob sql.NullInt64
		var firstSeen, lastSeen int64
		if err := rows.Scan(
			&item.ID, &item.PostID, &item.Author, &item.PostURL, &item.PageURL,
			&postCreated, &item.Note, &firstSeen, &lastSeen, &item.MediaCount,
			&item.JobCount, &item.Completed, &item.Failed, &item.LatestStatus, &latestJob,
		); err != nil {
			rows.Close()
			return HistoryPage{}, err
		}
		item.FirstSeenAt = timeFromMilliseconds(firstSeen)
		item.LastSeenAt = timeFromMilliseconds(lastSeen)
		if postCreated.Valid {
			value := timeFromMilliseconds(postCreated.Int64)
			item.PostCreatedAt = &value
		}
		if latestJob.Valid {
			value := timeFromMilliseconds(latestJob.Int64)
			item.LatestJobAt = &value
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return HistoryPage{}, err
	}
	rows.Close()

	hasMore := len(items) > query.Limit
	if hasMore {
		items = items[:query.Limit]
	}
	for index := range items {
		tags, err := database.TagsForHistoryItem(ctx, items[index].ID)
		if err != nil {
			return HistoryPage{}, err
		}
		items[index].Tags = tags
	}
	page := HistoryPage{Items: items, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		page.NextCursor = encodeHistoryCursor(last.LastSeenAt.UnixMilli(), last.ID)
	}
	return page, nil
}

func (database *Database) UpdateHistoryNote(ctx context.Context, id int64, note string) error {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 2000 {
		return errors.New("note must not exceed 2000 characters")
	}
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, "UPDATE library_items SET note = ? WHERE id = ?", note, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("history item not found")
	}
	if err := refreshSearchDocumentTx(ctx, tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (database *Database) DeleteHistoryItem(ctx context.Context, id int64) error {
	result, err := database.db.ExecContext(ctx, "DELETE FROM library_items WHERE id = ?", id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return errors.New("history item not found")
	}
	return nil
}

func quoteFTSQuery(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func encodeHistoryCursor(timestamp, id int64) string {
	payload, _ := json.Marshal([]int64{timestamp, id})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeHistoryCursor(value string) (int64, int64, bool) {
	if value == "" {
		return 0, 0, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, 0, false
	}
	var parts []int64
	if json.Unmarshal(payload, &parts) != nil || len(parts) != 2 {
		return 0, 0, false
	}
	return parts[0], parts[1], true
}

func ParseHistoryQuery(values url.Values) (HistoryQuery, error) {
	query := HistoryQuery{Query: first(values["query"]), Status: first(values["status"]), Cursor: first(values["cursor"])}
	if query.Status != "" && !map[string]bool{
		"queued": true, "downloading": true, "completed": true, "failed": true, "cancelled": true,
	}[query.Status] {
		return HistoryQuery{}, errors.New("unknown job status")
	}
	if query.Cursor != "" {
		if _, _, ok := decodeHistoryCursor(query.Cursor); !ok {
			return HistoryQuery{}, errors.New("invalid history cursor")
		}
	}
	if value := first(values["limit"]); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil {
			return HistoryQuery{}, errors.New("limit must be an integer")
		}
		query.Limit = limit
	}
	if value := first(values["tagId"]); value != "" {
		tagID, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return HistoryQuery{}, errors.New("tagId must be an integer")
		}
		query.TagID = tagID
	}
	for value, destination := range map[string]*time.Time{"from": &query.From, "to": &query.To} {
		text := first(values[value])
		if text == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return HistoryQuery{}, fmt.Errorf("%s must use RFC3339 format", value)
		}
		*destination = parsed
	}
	if !query.From.IsZero() && !query.To.IsZero() && !query.From.Before(query.To) {
		return HistoryQuery{}, errors.New("from must be earlier than to")
	}
	return query, nil
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

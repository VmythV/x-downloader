package storage

import (
	"context"
	"time"
)

type Statistics struct {
	GeneratedAt time.Time         `json:"generatedAt"`
	Summary     StatisticsSummary `json:"summary"`
	Daily       []DailyStatistic  `json:"daily"`
	Errors      []RankedStatistic `json:"errors"`
	Authors     []RankedStatistic `json:"authors"`
	Tags        []RankedStatistic `json:"tags"`
	Resolutions []RankedStatistic `json:"resolutions"`
}

type StatisticsSummary struct {
	HistoryItems   int     `json:"historyItems"`
	MediaItems     int     `json:"mediaItems"`
	Jobs           int     `json:"jobs"`
	Completed      int     `json:"completed"`
	Failed         int     `json:"failed"`
	Cancelled      int     `json:"cancelled"`
	Active         int     `json:"active"`
	Attempts       int     `json:"attempts"`
	TotalBytes     int64   `json:"totalBytes"`
	SuccessRate    float64 `json:"successRate"`
	AverageSeconds float64 `json:"averageSeconds"`
}

type DailyStatistic struct {
	Date      string `json:"date"`
	Completed int    `json:"completed"`
	Failed    int    `json:"failed"`
	Bytes     int64  `json:"bytes"`
}

type RankedStatistic struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func (database *Database) Statistics(ctx context.Context) (Statistics, error) {
	result := Statistics{GeneratedAt: time.Now().UTC()}
	row := database.db.QueryRowContext(ctx, `
SELECT
  (SELECT COUNT(*) FROM library_items),
  (SELECT COUNT(*) FROM media_items),
  COUNT(*),
  COUNT(CASE WHEN status = 'completed' THEN 1 END),
  COUNT(CASE WHEN status = 'failed' THEN 1 END),
  COUNT(CASE WHEN status = 'cancelled' THEN 1 END),
  COUNT(CASE WHEN status IN ('queued', 'downloading') THEN 1 END),
  (SELECT COUNT(*) FROM job_attempts),
  COALESCE(SUM(CASE WHEN status = 'completed' THEN file_size ELSE 0 END), 0),
  COALESCE(AVG(CASE WHEN status = 'completed' AND finished_at IS NOT NULL AND started_at IS NOT NULL
                    THEN (finished_at - started_at) / 1000.0 END), 0)
FROM download_jobs`)
	if err := row.Scan(
		&result.Summary.HistoryItems, &result.Summary.MediaItems, &result.Summary.Jobs,
		&result.Summary.Completed, &result.Summary.Failed, &result.Summary.Cancelled,
		&result.Summary.Active, &result.Summary.Attempts, &result.Summary.TotalBytes,
		&result.Summary.AverageSeconds,
	); err != nil {
		return Statistics{}, err
	}
	terminal := result.Summary.Completed + result.Summary.Failed
	if terminal > 0 {
		result.Summary.SuccessRate = float64(result.Summary.Completed) / float64(terminal) * 100
	}

	dailyRows, err := database.db.QueryContext(ctx, `
SELECT strftime('%Y-%m-%d', finished_at / 1000, 'unixepoch', 'localtime') AS day,
       COUNT(CASE WHEN status = 'completed' THEN 1 END),
       COUNT(CASE WHEN status = 'failed' THEN 1 END),
       COALESCE(SUM(CASE WHEN status = 'completed' THEN file_size ELSE 0 END), 0)
FROM download_jobs
WHERE finished_at IS NOT NULL AND finished_at >= ?
GROUP BY day ORDER BY day`, time.Now().AddDate(0, 0, -29).UnixMilli())
	if err != nil {
		return Statistics{}, err
	}
	for dailyRows.Next() {
		var item DailyStatistic
		if err := dailyRows.Scan(&item.Date, &item.Completed, &item.Failed, &item.Bytes); err != nil {
			dailyRows.Close()
			return Statistics{}, err
		}
		result.Daily = append(result.Daily, item)
	}
	dailyRows.Close()

	if result.Errors, err = database.ranking(ctx, `
SELECT error_code, COUNT(*) FROM download_jobs
WHERE error_code <> '' AND error_code <> 'cancelled'
GROUP BY error_code ORDER BY COUNT(*) DESC LIMIT 8`); err != nil {
		return Statistics{}, err
	}
	if result.Authors, err = database.ranking(ctx, `
SELECT CASE WHEN author = '' THEN '未知作者' ELSE author END, COUNT(*)
FROM library_items GROUP BY author ORDER BY COUNT(*) DESC LIMIT 8`); err != nil {
		return Statistics{}, err
	}
	if result.Tags, err = database.ranking(ctx, `
SELECT t.name, COUNT(*) FROM tags t
JOIN library_item_tags lit ON lit.tag_id = t.id
GROUP BY t.id ORDER BY COUNT(*) DESC LIMIT 8`); err != nil {
		return Statistics{}, err
	}
	if result.Resolutions, err = database.ranking(ctx, `
SELECT CASE WHEN height > 0 THEN CAST(height AS TEXT) || 'p' ELSE '未知' END, COUNT(*)
FROM download_jobs GROUP BY height ORDER BY COUNT(*) DESC LIMIT 8`); err != nil {
		return Statistics{}, err
	}
	return result, nil
}

func (database *Database) ranking(ctx context.Context, statement string) ([]RankedStatistic, error) {
	rows, err := database.db.QueryContext(ctx, statement)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RankedStatistic, 0)
	for rows.Next() {
		var item RankedStatistic
		if err := rows.Scan(&item.Name, &item.Count); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

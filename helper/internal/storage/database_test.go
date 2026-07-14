package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"x-downloader/helper/internal/hls"
	"x-downloader/helper/internal/jobs"
	"x-downloader/helper/internal/media"
	"x-downloader/helper/internal/settings"
)

func testDefaults(root string) settings.Values {
	return settings.Values{
		DownloadDir:      filepath.Join(root, "downloads"),
		FilenameTemplate: "{postId}_{mediaId}_{height}p.{ext}",
		Concurrency:      2,
		RetryCount:       1,
	}
}

func testCandidate(id, mediaID, postID, author string, discoveredAt time.Time) media.Candidate {
	return media.Candidate{
		ID: id, MediaID: mediaID,
		MasterURL: "https://video.twimg.com/amplify_video/" + mediaID + "/pl/master.m3u8",
		UserAgent: "Mozilla/5.0 Test",
		Context: media.Context{
			PageURL: "https://x.com/home", PostURL: "https://x.com/" + author + "/status/" + postID,
			PostID: postID, Author: author, MediaIndex: 1,
		},
		DiscoveredAt: discoveredAt,
		Variants: []hls.Variant{{
			ID: "720p", URL: "https://video.twimg.com/amplify_video/" + mediaID + "/pl/720x1280/video.m3u8",
			Width: 1280, Height: 720, Bandwidth: 1800000,
			Audio: &hls.Audio{URL: "https://video.twimg.com/amplify_video/" + mediaID + "/pl/audio.m3u8", Bitrate: 128000},
		}},
	}
}

func openTestDatabase(t *testing.T) (*Database, string) {
	t.Helper()
	root := t.TempDir()
	database, err := Open(filepath.Join(root, "state", "x-downloader.sqlite3"), LegacyPaths{Defaults: testDefaults(root)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return database, root
}

func TestDatabaseHistorySearchTagsStatisticsAndCascade(t *testing.T) {
	database, root := openTestDatabase(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	candidate := testCandidate("media-1001", "1001", "9001", "测试作者", now)
	if err := database.UpsertCandidates([]media.Candidate{candidate}); err != nil {
		t.Fatal(err)
	}

	outputPath := filepath.Join(root, "downloads", "9001_1001_720p.mp4")
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, []byte("downloaded video"), 0o600); err != nil {
		t.Fatal(err)
	}
	startedAt := now.Add(time.Second)
	finishedAt := startedAt.Add(4 * time.Second)
	stored := jobs.StoredJob{
		Job: jobs.Job{
			ID: "job-1", CandidateID: candidate.ID, VariantID: "720p", MediaID: candidate.MediaID,
			Width: 1280, Height: 720, Status: "completed", OutputPath: outputPath,
			Attempt: 1, MaxAttempts: 2, CreatedAt: now, StartedAt: &startedAt, FinishedAt: &finishedAt,
			Progress: jobs.Progress{DurationSeconds: 4, Percent: 100, Phase: "completed"}, Revision: 3,
		},
		Candidate: candidate, Variant: candidate.Variants[0], TempPath: filepath.Join(root, ".partial", "job-1.part.mp4"),
	}
	if err := database.UpsertJobs([]jobs.StoredJob{stored}); err != nil {
		t.Fatal(err)
	}

	page, err := database.SearchHistory(ctx, HistoryQuery{Query: "测试作", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Completed != 1 || page.Items[0].LatestStatus != "completed" {
		t.Fatalf("unexpected history page: %#v", page)
	}
	historyID := page.Items[0].ID
	if err := database.UpdateHistoryNote(ctx, historyID, "稍后整理收藏"); err != nil {
		t.Fatal(err)
	}
	notePage, err := database.SearchHistory(ctx, HistoryQuery{Query: "整理收藏", Limit: 20})
	if err != nil || len(notePage.Items) != 1 {
		t.Fatalf("note search failed: page=%#v err=%v", notePage, err)
	}

	tag, err := database.CreateTag(ctx, "收藏", "#ff8800")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AssignTag(ctx, historyID, tag.ID); err != nil {
		t.Fatal(err)
	}
	tagged, err := database.SearchHistory(ctx, HistoryQuery{TagID: tag.ID, Status: "completed", Limit: 20})
	if err != nil || len(tagged.Items) != 1 || len(tagged.Items[0].Tags) != 1 {
		t.Fatalf("tag filter failed: page=%#v err=%v", tagged, err)
	}

	statistics, err := database.Statistics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if statistics.Summary.HistoryItems != 1 || statistics.Summary.Jobs != 1 || statistics.Summary.Completed != 1 || statistics.Summary.TotalBytes == 0 {
		t.Fatalf("unexpected statistics: %#v", statistics.Summary)
	}
	jobPage, err := database.SearchJobs(ctx, JobQuery{Status: "completed", Query: "9001_1001", Limit: 1})
	if err != nil || len(jobPage.Items) != 1 || jobPage.Items[0].ID != "job-1" {
		t.Fatalf("task history search failed: page=%#v err=%v", jobPage, err)
	}
	if err := database.DeleteHistoryItem(ctx, historyID); err != nil {
		t.Fatal(err)
	}
	stats, err := database.JobStats()
	if err != nil || stats["total"] != 0 {
		t.Fatalf("history deletion did not cascade to jobs: stats=%v err=%v", stats, err)
	}
}

func TestHistoryCursorPagination(t *testing.T) {
	database, _ := openTestDatabase(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for index, candidate := range []media.Candidate{
		testCandidate("media-2001", "2001", "9101", "first", now),
		testCandidate("media-2002", "2002", "9102", "second", now.Add(time.Second)),
	} {
		if err := database.UpsertCandidates([]media.Candidate{candidate}); err != nil {
			t.Fatalf("candidate %d: %v", index, err)
		}
	}
	first, err := database.SearchHistory(context.Background(), HistoryQuery{Limit: 1})
	if err != nil || len(first.Items) != 1 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("unexpected first page: %#v err=%v", first, err)
	}
	second, err := database.SearchHistory(context.Background(), HistoryQuery{Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 1 || second.HasMore || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("unexpected second page: %#v err=%v", second, err)
	}
}

func TestCandidateContextMoveDoesNotLeaveEmptyHistory(t *testing.T) {
	database, _ := openTestDatabase(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	candidate := testCandidate("media-2501", "2501", "", "", now)
	candidate.Context.PostURL = ""
	if err := database.UpsertCandidates([]media.Candidate{candidate}); err != nil {
		t.Fatal(err)
	}
	candidate.Context.PostID = "9251"
	candidate.Context.Author = "linked"
	candidate.Context.PostURL = "https://x.com/linked/status/9251"
	if err := database.UpsertCandidates([]media.Candidate{candidate}); err != nil {
		t.Fatal(err)
	}
	page, err := database.SearchHistory(context.Background(), HistoryQuery{Limit: 20})
	if err != nil || len(page.Items) != 1 || page.Items[0].PostID != "9251" {
		t.Fatalf("candidate move left an empty history row: page=%#v err=%v", page, err)
	}
}

func TestLegacyJSONMigrationCreatesBackups(t *testing.T) {
	root := t.TempDir()
	candidatePath := filepath.Join(root, "candidates.json")
	jobPath := filepath.Join(root, "jobs.json")
	settingsPath := filepath.Join(root, "settings.json")
	now := time.Now().UTC().Truncate(time.Millisecond)
	candidate := testCandidate("media-3001", "3001", "9201", "legacy", now)
	finishedAt := now.Add(time.Minute)
	job := jobs.StoredJob{Job: jobs.Job{
		ID: "legacy-job", CandidateID: candidate.ID, VariantID: "720p", MediaID: candidate.MediaID,
		Width: 1280, Height: 720, Status: "failed", Error: "network timeout",
		Attempt: 2, MaxAttempts: 2, CreatedAt: now, FinishedAt: &finishedAt,
	}, Candidate: candidate, Variant: candidate.Variants[0], TempPath: filepath.Join(root, "legacy.part.mp4")}
	writeJSONFile := func(path string, value any) {
		t.Helper()
		content, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeJSONFile(candidatePath, legacyCandidates{Version: 1, Candidates: []media.Candidate{candidate}})
	writeJSONFile(jobPath, legacyJobs{Version: 1, Jobs: []jobs.StoredJob{job}})
	writeJSONFile(settingsPath, legacySettings{Version: 2, Values: settings.Values{
		DownloadDir: filepath.Join(root, "chosen"), FilenameTemplate: "{mediaId}.{ext}", Concurrency: 3, RetryCount: 2,
	}})

	database, err := Open(filepath.Join(root, "x-downloader.sqlite3"), LegacyPaths{
		Candidates: candidatePath, Jobs: jobPath, Settings: settingsPath, Defaults: testDefaults(root),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if count, err := database.CandidateCount(); err != nil || count != 1 {
		t.Fatalf("candidate migration failed: count=%d err=%v", count, err)
	}
	if stats, err := database.JobStats(); err != nil || stats["failed"] != 1 {
		t.Fatalf("job migration failed: stats=%v err=%v", stats, err)
	}
	values, found, err := database.LoadSettings()
	if err != nil || !found || values.DownloadDir != filepath.Join(root, "chosen") || values.Concurrency != 3 {
		t.Fatalf("settings migration failed: values=%#v found=%t err=%v", values, found, err)
	}
	for _, path := range []string{candidatePath, jobPath, settingsPath} {
		if _, err := os.Stat(path + ".migrated-v1.bak"); err != nil {
			t.Errorf("missing migration backup for %s: %v", filepath.Base(path), err)
		}
	}
}

func TestDatabaseStoresTenThousandTasksWithPagination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping SQLite capacity test in short mode")
	}
	database, _ := openTestDatabase(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	candidate := testCandidate("media-10000", "10000", "9900", "bulk", now)
	if err := database.UpsertCandidates([]media.Candidate{candidate}); err != nil {
		t.Fatal(err)
	}
	finishedAt := now.Add(time.Minute)
	items := make([]jobs.StoredJob, 10_000)
	for index := range items {
		items[index] = jobs.StoredJob{
			Job: jobs.Job{
				ID: fmt.Sprintf("bulk-%05d", index), CandidateID: candidate.ID, VariantID: "720p",
				MediaID: candidate.MediaID, Width: 1280, Height: 720, Status: "failed",
				Error: "network timeout", Attempt: 1, MaxAttempts: 1,
				CreatedAt: now.Add(time.Duration(index) * time.Millisecond), FinishedAt: &finishedAt,
			},
			Variant: candidate.Variants[0],
		}
	}
	if err := database.UpsertJobs(items); err != nil {
		t.Fatal(err)
	}
	stats, err := database.JobStats()
	if err != nil || stats["total"] != 10_000 || stats["failed"] != 10_000 {
		t.Fatalf("unexpected bulk task stats: stats=%v err=%v", stats, err)
	}
	page, err := database.SearchJobs(ctx, JobQuery{Status: "failed", Limit: 100})
	if err != nil || len(page.Items) != 100 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("unexpected bulk task page: items=%d hasMore=%t cursor=%q err=%v", len(page.Items), page.HasMore, page.NextCursor, err)
	}
	next, err := database.SearchJobs(ctx, JobQuery{Status: "failed", Cursor: page.NextCursor, Limit: 100})
	if err != nil || len(next.Items) != 100 || next.Items[0].ID == page.Items[0].ID {
		t.Fatalf("unexpected second bulk task page: items=%d err=%v", len(next.Items), err)
	}
}

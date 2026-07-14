package jobs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"x-downloader/helper/internal/hls"
	"x-downloader/helper/internal/media"
)

type fakeCandidates struct{ candidate media.Candidate }

func (source fakeCandidates) Get(id string) (media.Candidate, error) { return source.candidate, nil }

type fakeRunner struct{}

func (fakeRunner) Run(_ context.Context, spec DownloadSpec, onProgress func(Progress)) error {
	onProgress(Progress{OutTimeSeconds: 3, Speed: "10x"})
	return os.WriteFile(spec.OutputPath, []byte("mp4"), 0o600)
}

func TestManagerDownloadsAndNamesCandidate(t *testing.T) {
	audio := &hls.Audio{URL: "https://video.twimg.com/audio.m3u8", Bitrate: 128000}
	candidate := media.Candidate{
		ID: "media-123", MediaID: "123",
		Context:  media.Context{Author: "test user", PostID: "456", MediaIndex: 2, CreatedAt: time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)},
		Variants: []hls.Variant{{ID: "1280x720-1", URL: "https://video.twimg.com/video.m3u8", Width: 1280, Height: 720, Audio: audio}},
	}
	downloadDir := filepath.Join(t.TempDir(), "downloads")
	tempDir := filepath.Join(t.TempDir(), "temp")
	manager, err := NewManager(1, downloadDir, tempDir, "{date}_{author}_{postId}_{mediaIndex}_{mediaId}_{height}p.{ext}", fakeCandidates{candidate}, fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	job, err := manager.Submit(candidate.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, err = manager.Get(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job.Status != "completed" {
		t.Fatalf("job did not complete: %+v", job)
	}
	if filepath.Base(job.OutputPath) != "2026-07-13_test_user_456_02_123_720p.mp4" {
		t.Fatalf("unexpected output filename: %s", job.OutputPath)
	}
}

func TestPersistentManagerRestoresCompletedHistory(t *testing.T) {
	audio := &hls.Audio{URL: "https://video.twimg.com/audio.m3u8"}
	candidate := media.Candidate{
		ID: "media-123", MediaID: "123",
		Variants: []hls.Variant{{ID: "highest", URL: "https://video.twimg.com/video.m3u8", Width: 1280, Height: 720, Audio: audio}},
	}
	root := t.TempDir()
	statePath := filepath.Join(root, "state", "jobs.json")
	manager, err := NewPersistentManager(1, filepath.Join(root, "downloads"), filepath.Join(root, "temp"), "{mediaId}_{height}p.{ext}", statePath, 10, fakeCandidates{candidate}, fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	job, err := manager.Submit(candidate.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, _ = manager.Get(job.ID)
		if job.Status == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	restored, err := NewPersistentManager(1, filepath.Join(root, "downloads"), filepath.Join(root, "temp"), "{mediaId}_{height}p.{ext}", statePath, 10, fakeCandidates{candidate}, fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	items := restored.List()
	if len(items) != 1 || items[0].Status != "completed" {
		t.Fatalf("unexpected restored jobs: %+v", items)
	}
}

package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"x-downloader/helper/internal/hls"
	"x-downloader/helper/internal/media"
)

type CandidateSource interface {
	Get(id string) (media.Candidate, error)
}

var (
	ErrJobNotFound = errors.New("download job not found")
	ErrQueueFull   = errors.New("download queue is full")
)

type DownloadSpec struct {
	VideoURL   string
	AudioURL   string
	OutputPath string
}

type Progress struct {
	OutTimeSeconds float64 `json:"outTimeSeconds,omitempty"`
	Speed          string  `json:"speed,omitempty"`
}

type Runner interface {
	Run(ctx context.Context, spec DownloadSpec, onProgress func(Progress)) error
}

type Job struct {
	ID          string     `json:"id"`
	CandidateID string     `json:"candidateId"`
	VariantID   string     `json:"variantId"`
	MediaID     string     `json:"mediaId"`
	Width       int        `json:"width,omitempty"`
	Height      int        `json:"height,omitempty"`
	Status      string     `json:"status"`
	Progress    Progress   `json:"progress"`
	OutputPath  string     `json:"outputPath,omitempty"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
}

type jobState struct {
	job       Job
	candidate media.Candidate
	variant   hls.Variant
	tempPath  string
	cancel    context.CancelFunc
}

type Manager struct {
	mu               sync.RWMutex
	jobs             map[string]*jobState
	bySelection      map[string]string
	queue            chan string
	candidates       CandidateSource
	runner           Runner
	downloadDir      string
	tempDir          string
	filenameTemplate string
}

func NewManager(concurrency int, downloadDir, tempDir, filenameTemplate string, candidates CandidateSource, runner Runner) (*Manager, error) {
	if concurrency < 1 || concurrency > 4 {
		return nil, errors.New("job concurrency must be between 1 and 4")
	}
	if candidates == nil || runner == nil {
		return nil, errors.New("candidate source and runner are required")
	}
	if err := os.MkdirAll(downloadDir, 0o755); err != nil {
		return nil, fmt.Errorf("create download directory: %w", err)
	}
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return nil, fmt.Errorf("create temporary directory: %w", err)
	}
	manager := &Manager{
		jobs:             make(map[string]*jobState),
		bySelection:      make(map[string]string),
		queue:            make(chan string, 100),
		candidates:       candidates,
		runner:           runner,
		downloadDir:      downloadDir,
		tempDir:          tempDir,
		filenameTemplate: filenameTemplate,
	}
	for range concurrency {
		go manager.worker()
	}
	return manager, nil
}

func (manager *Manager) Submit(candidateID, variantID string) (Job, error) {
	candidate, err := manager.candidates.Get(candidateID)
	if err != nil {
		return Job{}, err
	}
	variant, err := selectVariant(candidate.Variants, variantID)
	if err != nil {
		return Job{}, err
	}
	if variant.Audio == nil || variant.Audio.URL == "" {
		return Job{}, errors.New("selected video variant has no associated audio rendition")
	}
	selectionKey := candidate.ID + "|" + variant.ID

	manager.mu.Lock()
	if existingID := manager.bySelection[selectionKey]; existingID != "" {
		if existing := manager.jobs[existingID]; existing != nil {
			reusable := existing.job.Status != "failed" && existing.job.Status != "cancelled"
			if existing.job.Status == "completed" {
				_, statErr := os.Stat(existing.job.OutputPath)
				reusable = statErr == nil
			}
			if reusable {
				job := existing.job
				manager.mu.Unlock()
				slog.Info("download job reused", "jobId", job.ID, "candidateId", job.CandidateID, "variantId", job.VariantID, "status", job.Status)
				return job, nil
			}
		}
	}

	id, err := randomID()
	if err != nil {
		manager.mu.Unlock()
		return Job{}, err
	}
	filename := buildFilename(manager.filenameTemplate, candidate, variant, time.Now())
	outputPath := filepath.Join(manager.downloadDir, filename)
	tempPath := filepath.Join(manager.tempDir, id+".part.mp4")
	now := time.Now().UTC()
	state := &jobState{
		job: Job{
			ID: id, CandidateID: candidate.ID, VariantID: variant.ID, MediaID: candidate.MediaID,
			Width: variant.Width, Height: variant.Height, Status: "queued", OutputPath: outputPath, CreatedAt: now,
		},
		candidate: candidate,
		variant:   variant,
		tempPath:  tempPath,
	}
	if _, err := os.Stat(outputPath); err == nil {
		state.job.Status = "completed"
		state.job.StartedAt = &now
		state.job.FinishedAt = &now
	}
	manager.jobs[id] = state
	manager.bySelection[selectionKey] = id
	job := state.job
	manager.mu.Unlock()

	if job.Status == "queued" {
		select {
		case manager.queue <- id:
			slog.Info("download job queued", "jobId", job.ID, "candidateId", job.CandidateID, "variantId", job.VariantID, "resolution", fmt.Sprintf("%dx%d", job.Width, job.Height))
		default:
			manager.mu.Lock()
			delete(manager.jobs, id)
			if manager.bySelection[selectionKey] == id {
				delete(manager.bySelection, selectionKey)
			}
			manager.mu.Unlock()
			slog.Warn("download queue full", "candidateId", candidate.ID, "variantId", variant.ID)
			return Job{}, ErrQueueFull
		}
	} else {
		slog.Info("download output already exists", "jobId", job.ID, "outputPath", job.OutputPath)
	}
	return job, nil
}

func (manager *Manager) Get(id string) (Job, error) {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	state := manager.jobs[id]
	if state == nil {
		return Job{}, ErrJobNotFound
	}
	return state.job, nil
}

func (manager *Manager) List() []Job {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	result := make([]Job, 0, len(manager.jobs))
	for _, state := range manager.jobs {
		result = append(result, state.job)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}

func (manager *Manager) Cancel(id string) (Job, error) {
	manager.mu.Lock()
	state := manager.jobs[id]
	if state == nil {
		manager.mu.Unlock()
		return Job{}, ErrJobNotFound
	}
	switch state.job.Status {
	case "queued":
		now := time.Now().UTC()
		state.job.Status = "cancelled"
		state.job.FinishedAt = &now
		slog.Info("queued download cancelled", "jobId", state.job.ID, "candidateId", state.job.CandidateID)
	case "downloading":
		if state.cancel != nil {
			state.cancel()
			slog.Info("download cancellation requested", "jobId", state.job.ID, "candidateId", state.job.CandidateID)
		}
	}
	job := state.job
	manager.mu.Unlock()
	return job, nil
}

func (manager *Manager) worker() {
	for id := range manager.queue {
		manager.run(id)
	}
}

func (manager *Manager) run(id string) {
	manager.mu.Lock()
	state := manager.jobs[id]
	if state == nil || state.job.Status != "queued" {
		manager.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().UTC()
	state.cancel = cancel
	state.job.Status = "downloading"
	state.job.StartedAt = &now
	videoURL := state.variant.URL
	audioURL := state.variant.Audio.URL
	tempPath := state.tempPath
	jobID := state.job.ID
	candidateID := state.job.CandidateID
	variantID := state.job.VariantID
	outputPath := state.job.OutputPath
	manager.mu.Unlock()

	slog.Info("download started", "jobId", jobID, "candidateId", candidateID, "variantId", variantID, "outputPath", outputPath)

	err := manager.runner.Run(ctx, DownloadSpec{
		VideoURL: videoURL, AudioURL: audioURL, OutputPath: tempPath,
	}, func(progress Progress) {
		manager.mu.Lock()
		if current := manager.jobs[id]; current != nil {
			current.job.Progress = progress
		}
		manager.mu.Unlock()
	})

	manager.mu.Lock()
	defer manager.mu.Unlock()
	state = manager.jobs[id]
	if state == nil {
		return
	}
	state.cancel = nil
	finishedAt := time.Now().UTC()
	state.job.FinishedAt = &finishedAt
	if errors.Is(err, context.Canceled) {
		state.job.Status = "cancelled"
		_ = os.Remove(tempPath)
		slog.Info("download cancelled", "jobId", jobID, "candidateId", candidateID)
		return
	}
	if err != nil {
		state.job.Status = "failed"
		state.job.Error = err.Error()
		_ = os.Remove(tempPath)
		slog.Warn("download failed", "jobId", jobID, "candidateId", candidateID, "error", summarizeDownloadError(err))
		return
	}
	if _, err := os.Stat(state.job.OutputPath); err == nil {
		_ = os.Remove(tempPath)
		state.job.Status = "completed"
		slog.Info("download completed; output already present", "jobId", jobID, "outputPath", outputPath)
		return
	}
	if err := os.Rename(tempPath, state.job.OutputPath); err != nil {
		state.job.Status = "failed"
		state.job.Error = fmt.Sprintf("move completed file: %v", err)
		_ = os.Remove(tempPath)
		slog.Warn("download finalization failed", "jobId", jobID, "error", err)
		return
	}
	state.job.Status = "completed"
	slog.Info("download completed", "jobId", jobID, "candidateId", candidateID, "outputPath", outputPath)
}

func summarizeDownloadError(err error) string {
	message := err.Error()
	for {
		start := strings.Index(message, "https://video.twimg.com/")
		if start < 0 {
			return message
		}
		end := start
		for end < len(message) && !strings.ContainsRune(" \t\r\n\"'", rune(message[end])) {
			end++
		}
		message = message[:start] + "<redacted-video-url>" + message[end:]
	}
}

func selectVariant(variants []hls.Variant, requestedID string) (hls.Variant, error) {
	if len(variants) == 0 {
		return hls.Variant{}, errors.New("media candidate has no video variants")
	}
	if requestedID == "" {
		return variants[0], nil
	}
	for _, variant := range variants {
		if variant.ID == requestedID {
			return variant, nil
		}
	}
	return hls.Variant{}, errors.New("requested video variant was not found")
}

func buildFilename(template string, candidate media.Candidate, variant hls.Variant, fallbackTime time.Time) string {
	createdAt := candidate.Context.CreatedAt
	if createdAt.IsZero() {
		createdAt = fallbackTime
	}
	author := sanitizeComponent(candidate.Context.Author, 48)
	if author == "" {
		author = "unknown"
	}
	postID := candidate.Context.PostID
	if postID == "" {
		postID = "post"
	}
	mediaIndex := "01"
	if candidate.Context.MediaIndex > 0 {
		mediaIndex = fmt.Sprintf("%02d", candidate.Context.MediaIndex)
	}
	replacements := map[string]string{
		"{date}":       createdAt.Format("2006-01-02"),
		"{author}":     author,
		"{postId}":     postID,
		"{mediaIndex}": mediaIndex,
		"{mediaId}":    candidate.MediaID,
		"{width}":      strconv.Itoa(variant.Width),
		"{height}":     strconv.Itoa(variant.Height),
		"{ext}":        "mp4",
	}
	filename := template
	for placeholder, value := range replacements {
		filename = strings.ReplaceAll(filename, placeholder, value)
	}
	filename = sanitizeComponent(filename, 180)
	if !strings.HasSuffix(strings.ToLower(filename), ".mp4") {
		filename += ".mp4"
	}
	if filename == ".mp4" {
		filename = fmt.Sprintf("x_%s_%dp.mp4", candidate.MediaID, variant.Height)
	}
	return filename
}

func sanitizeComponent(value string, maxRunes int) string {
	var builder strings.Builder
	lastUnderscore := false
	count := 0
	for _, char := range strings.TrimSpace(value) {
		if count >= maxRunes {
			break
		}
		switch {
		case unicode.IsLetter(char), unicode.IsDigit(char), strings.ContainsRune("-_.", char):
			builder.WriteRune(char)
			lastUnderscore = false
			count++
		case unicode.IsSpace(char):
			if !lastUnderscore && builder.Len() > 0 {
				builder.WriteByte('_')
				lastUnderscore = true
				count++
			}
		}
	}
	result := strings.Trim(builder.String(), " ._")
	for len(result) > 0 && !utf8.ValidString(result) {
		result = result[:len(result)-1]
	}
	return result
}

func randomID() (string, error) {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

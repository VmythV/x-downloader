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

	"x-downloader/helper/internal/downloadpath"
	"x-downloader/helper/internal/hls"
	"x-downloader/helper/internal/media"
	"x-downloader/helper/internal/statefile"
)

const (
	defaultMaxJobs = 500
	maxConcurrency = 4
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
	UserAgent  string
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
	Attempt     int        `json:"attempt,omitempty"`
	MaxAttempts int        `json:"maxAttempts,omitempty"`
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
	persistMu        sync.Mutex
	limitMu          sync.Mutex
	limitCond        *sync.Cond
	concurrency      int
	activeWorkers    int
	jobs             map[string]*jobState
	bySelection      map[string]string
	queue            chan string
	candidates       CandidateSource
	runner           Runner
	downloadDir      string
	tempDir          string
	filenameTemplate string
	retryCount       int
	retryDelay       func(int) time.Duration
	stateFile        string
	maxJobs          int
}

type persistedJob struct {
	Job       Job             `json:"job"`
	Candidate media.Candidate `json:"candidate"`
	Variant   hls.Variant     `json:"variant"`
	TempPath  string          `json:"tempPath"`
}

type persistedState struct {
	Version int            `json:"version"`
	Jobs    []persistedJob `json:"jobs"`
}

func NewManager(concurrency int, downloadDir, tempDir, filenameTemplate string, candidates CandidateSource, runner Runner) (*Manager, error) {
	return newManager(concurrency, downloadDir, tempDir, filenameTemplate, "", defaultMaxJobs, candidates, runner)
}

func NewPersistentManager(concurrency int, downloadDir, tempDir, filenameTemplate, stateFile string, maxJobs int, candidates CandidateSource, runner Runner) (*Manager, error) {
	if maxJobs <= 0 {
		maxJobs = defaultMaxJobs
	}
	return newManager(concurrency, downloadDir, tempDir, filenameTemplate, stateFile, maxJobs, candidates, runner)
}

func newManager(concurrency int, downloadDir, tempDir, filenameTemplate, stateFile string, maxJobs int, candidates CandidateSource, runner Runner) (*Manager, error) {
	if concurrency < 1 || concurrency > 4 {
		return nil, errors.New("job concurrency must be between 1 and 4")
	}
	if candidates == nil || runner == nil {
		return nil, errors.New("candidate source and runner are required")
	}
	downloadDir, err := downloadpath.Normalize(downloadDir)
	if err != nil {
		return nil, err
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
		concurrency:      concurrency,
		retryDelay: func(attempt int) time.Duration {
			return time.Duration(1<<min(max(attempt-1, 0), 3)) * time.Second
		},
		stateFile: stateFile,
		maxJobs:   maxJobs,
	}
	manager.limitCond = sync.NewCond(&manager.limitMu)
	if err := manager.restore(); err != nil {
		return nil, err
	}
	for range maxConcurrency {
		go manager.worker()
	}
	return manager, nil
}

func (manager *Manager) SetDownloadDir(path string) {
	manager.mu.Lock()
	manager.downloadDir = filepath.Clean(path)
	manager.mu.Unlock()
}

func (manager *Manager) SetFilenameTemplate(value string) {
	manager.mu.Lock()
	manager.filenameTemplate = value
	manager.mu.Unlock()
}

func (manager *Manager) SetConcurrency(value int) {
	if value < 1 || value > maxConcurrency {
		return
	}
	manager.limitMu.Lock()
	manager.concurrency = value
	manager.limitCond.Broadcast()
	manager.limitMu.Unlock()
}

func (manager *Manager) SetRetryCount(value int) {
	if value < 0 || value > 5 {
		return
	}
	manager.mu.Lock()
	manager.retryCount = value
	manager.mu.Unlock()
}

func (manager *Manager) restore() error {
	if manager.stateFile == "" {
		return nil
	}
	var saved persistedState
	if err := statefile.Read(manager.stateFile, &saved); err != nil {
		return fmt.Errorf("load job state: %w", err)
	}
	if saved.Version != 0 && saved.Version != 1 {
		return fmt.Errorf("unsupported job state version %d", saved.Version)
	}
	recovered := false
	for _, item := range saved.Jobs {
		if item.Job.ID == "" || item.Job.CandidateID == "" || item.Variant.ID == "" {
			continue
		}
		if item.Job.Status == "queued" || item.Job.Status == "downloading" {
			now := time.Now().UTC()
			item.Job.Status = "failed"
			item.Job.Error = "download interrupted because helper restarted"
			item.Job.FinishedAt = &now
			_ = os.Remove(item.TempPath)
			recovered = true
		}
		manager.jobs[item.Job.ID] = &jobState{
			job: item.Job, candidate: item.Candidate, variant: item.Variant, tempPath: item.TempPath,
		}
		selectionKey := item.Job.CandidateID + "|" + item.Job.VariantID
		existingID := manager.bySelection[selectionKey]
		if existingID == "" || manager.jobs[existingID].job.CreatedAt.Before(item.Job.CreatedAt) {
			manager.bySelection[selectionKey] = item.Job.ID
		}
	}
	manager.pruneLocked()
	if recovered {
		manager.persistBestEffort()
	}
	return nil
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
	manager.mu.RLock()
	downloadDir := manager.downloadDir
	manager.mu.RUnlock()
	downloadDir, err = downloadpath.Prepare(downloadDir)
	if err != nil {
		return Job{}, err
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
	outputPath := filepath.Join(downloadDir, filename)
	tempPath := filepath.Join(manager.tempDir, id+".part.mp4")
	now := time.Now().UTC()
	state := &jobState{
		job: Job{
			ID: id, CandidateID: candidate.ID, VariantID: variant.ID, MediaID: candidate.MediaID,
			Width: variant.Width, Height: variant.Height, Status: "queued", OutputPath: outputPath,
			MaxAttempts: manager.retryCount + 1, CreatedAt: now,
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
	manager.pruneLocked()
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
			manager.persistBestEffort()
			slog.Warn("download queue full", "candidateId", candidate.ID, "variantId", variant.ID)
			return Job{}, ErrQueueFull
		}
	} else {
		slog.Info("download output already exists", "jobId", job.ID, "outputPath", job.OutputPath)
	}
	manager.persistBestEffort()
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

func (manager *Manager) Stats() map[string]int {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	result := map[string]int{"total": len(manager.jobs)}
	for _, state := range manager.jobs {
		result[state.job.Status]++
	}
	return result
}

func (manager *Manager) pruneLocked() {
	if manager.maxJobs <= 0 || len(manager.jobs) <= manager.maxJobs {
		return
	}
	terminal := make([]*jobState, 0, len(manager.jobs))
	for _, state := range manager.jobs {
		if !strings.Contains("|completed|failed|cancelled|", "|"+state.job.Status+"|") {
			continue
		}
		terminal = append(terminal, state)
	}
	sort.Slice(terminal, func(i, j int) bool {
		return terminal[i].job.CreatedAt.Before(terminal[j].job.CreatedAt)
	})
	for _, state := range terminal {
		if len(manager.jobs) <= manager.maxJobs {
			break
		}
		delete(manager.jobs, state.job.ID)
		selectionKey := state.job.CandidateID + "|" + state.job.VariantID
		if manager.bySelection[selectionKey] == state.job.ID {
			delete(manager.bySelection, selectionKey)
		}
	}
}

func (manager *Manager) persistBestEffort() {
	if err := manager.persist(); err != nil {
		slog.Warn("persist job state", "error", err)
	}
}

func (manager *Manager) persist() error {
	if manager.stateFile == "" {
		return nil
	}
	manager.persistMu.Lock()
	defer manager.persistMu.Unlock()
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.persistLocked()
}

// persistLocked writes a snapshot while manager.mu is held. Callers must also
// hold persistMu so snapshots cannot be written out of order.
func (manager *Manager) persistLocked() error {
	items := make([]persistedJob, 0, len(manager.jobs))
	for _, state := range manager.jobs {
		items = append(items, persistedJob{
			Job: state.job, Candidate: state.candidate, Variant: state.variant, TempPath: state.tempPath,
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Job.CreatedAt.Before(items[j].Job.CreatedAt) })
	if err := statefile.Write(manager.stateFile, persistedState{Version: 1, Jobs: items}); err != nil {
		return fmt.Errorf("persist job state: %w", err)
	}
	return nil
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
	manager.persistBestEffort()
	return job, nil
}

func (manager *Manager) Reveal(id string) error {
	job, err := manager.Get(id)
	if err != nil {
		return err
	}
	if job.Status != "completed" || job.OutputPath == "" {
		return errors.New("only a completed download can be revealed")
	}
	if _, err := os.Stat(job.OutputPath); err != nil {
		return fmt.Errorf("downloaded file is unavailable: %w", err)
	}
	return revealFile(job.OutputPath)
}

func (manager *Manager) worker() {
	for id := range manager.queue {
		manager.acquireWorkerSlot()
		manager.run(id)
		manager.releaseWorkerSlot()
	}
}

func (manager *Manager) acquireWorkerSlot() {
	manager.limitMu.Lock()
	defer manager.limitMu.Unlock()
	for manager.activeWorkers >= manager.concurrency {
		manager.limitCond.Wait()
	}
	manager.activeWorkers++
}

func (manager *Manager) releaseWorkerSlot() {
	manager.limitMu.Lock()
	manager.activeWorkers--
	manager.limitCond.Broadcast()
	manager.limitMu.Unlock()
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
	state.job.Attempt++
	state.job.Error = ""
	state.job.Progress = Progress{}
	state.job.StartedAt = &now
	state.job.FinishedAt = nil
	videoURL := state.variant.URL
	audioURL := state.variant.Audio.URL
	tempPath := state.tempPath
	jobID := state.job.ID
	candidateID := state.job.CandidateID
	variantID := state.job.VariantID
	outputPath := state.job.OutputPath
	userAgent := state.candidate.UserAgent
	manager.mu.Unlock()
	manager.persistBestEffort()

	slog.Info("download started", "jobId", jobID, "candidateId", candidateID, "variantId", variantID, "outputPath", outputPath)

	err := manager.runner.Run(ctx, DownloadSpec{
		VideoURL: videoURL, AudioURL: audioURL, UserAgent: userAgent, OutputPath: tempPath,
	}, func(progress Progress) {
		manager.mu.Lock()
		if current := manager.jobs[id]; current != nil {
			current.job.Progress = progress
		}
		manager.mu.Unlock()
	})

	// Publish a terminal state only after its snapshot is durable. This prevents
	// a caller from observing "completed" while the state file still says the
	// job was downloading.
	manager.persistMu.Lock()
	manager.mu.Lock()
	state = manager.jobs[id]
	if state == nil {
		manager.mu.Unlock()
		manager.persistMu.Unlock()
		return
	}
	state.cancel = nil
	finishedAt := time.Now().UTC()
	state.job.FinishedAt = &finishedAt
	if errors.Is(err, context.Canceled) {
		state.job.Status = "cancelled"
		_ = os.Remove(tempPath)
		slog.Info("download cancelled", "jobId", jobID, "candidateId", candidateID)
	} else if err != nil && state.job.Attempt < state.job.MaxAttempts {
		attempt := state.job.Attempt
		maxAttempts := state.job.MaxAttempts
		state.job.Status = "queued"
		state.job.Error = ""
		state.job.FinishedAt = nil
		_ = os.Remove(tempPath)
		persistErr := manager.persistLocked()
		manager.mu.Unlock()
		manager.persistMu.Unlock()
		if persistErr != nil {
			slog.Warn("persist retried job state", "jobId", jobID, "error", persistErr)
		}
		slog.Warn("download attempt failed; retry scheduled", "jobId", jobID, "candidateId", candidateID, "attempt", attempt, "maxAttempts", maxAttempts, "error", summarizeDownloadError(err))
		manager.scheduleRetry(id, attempt)
		return
	} else if err != nil {
		state.job.Status = "failed"
		state.job.Error = err.Error()
		_ = os.Remove(tempPath)
		slog.Warn("download failed", "jobId", jobID, "candidateId", candidateID, "error", summarizeDownloadError(err))
	} else if _, statErr := os.Stat(state.job.OutputPath); statErr == nil {
		_ = os.Remove(tempPath)
		state.job.Status = "completed"
		slog.Info("download completed; output already present", "jobId", jobID, "outputPath", outputPath)
	} else if moveErr := os.Rename(tempPath, state.job.OutputPath); moveErr != nil {
		state.job.Status = "failed"
		state.job.Error = fmt.Sprintf("move completed file: %v", moveErr)
		_ = os.Remove(tempPath)
		slog.Warn("download finalization failed", "jobId", jobID, "error", moveErr)
	} else {
		state.job.Status = "completed"
		slog.Info("download completed", "jobId", jobID, "candidateId", candidateID, "outputPath", outputPath)
	}
	persistErr := manager.persistLocked()
	manager.mu.Unlock()
	manager.persistMu.Unlock()
	if persistErr != nil {
		slog.Warn("persist terminal job state", "jobId", jobID, "error", persistErr)
	}
}

func (manager *Manager) scheduleRetry(id string, attempt int) {
	time.AfterFunc(manager.retryDelay(attempt), func() {
		manager.mu.RLock()
		state := manager.jobs[id]
		queued := state != nil && state.job.Status == "queued"
		manager.mu.RUnlock()
		if queued {
			manager.queue <- id
		}
	})
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

package capture

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxPlaylistBytes    = 2 << 20
	maxObservationBatch = 20
	probeConcurrency    = 4
)

var (
	mediaIDPattern = regexp.MustCompile(`/(?:amplify_video|ext_tw_video)/(\d+)(?:/|$)`)
	videoPattern   = regexp.MustCompile(`/pl/(?:avc1|hvc1)/(\d+)x(\d+)/`)
	audioPattern   = regexp.MustCompile(`/pl/mp4a/(\d+)/`)
)

type Observation struct {
	URL         string    `json:"url"`
	SeenAt      time.Time `json:"seenAt"`
	PageURL     string    `json:"pageUrl,omitempty"`
	RequestType string    `json:"requestType,omitempty"`
}

type Probe struct {
	URL                   string    `json:"url"`
	MediaID               string    `json:"mediaId,omitempty"`
	Kind                  string    `json:"kind"`
	Classification        string    `json:"classification"`
	Width                 int       `json:"width,omitempty"`
	Height                int       `json:"height,omitempty"`
	Bitrate               int       `json:"bitrate,omitempty"`
	DurationSeconds       float64   `json:"durationSeconds,omitempty"`
	MasterVariantCount    int       `json:"masterVariantCount,omitempty"`
	MasterAudioRenditions int       `json:"masterAudioRenditions,omitempty"`
	StatusCode            int       `json:"statusCode,omitempty"`
	FetchedAt             time.Time `json:"fetchedAt"`
	StoredFile            string    `json:"storedFile,omitempty"`
	Error                 string    `json:"error,omitempty"`
}

type SessionSnapshot struct {
	ID           string        `json:"id"`
	Status       string        `json:"status"`
	StartedAt    time.Time     `json:"startedAt"`
	FinishedAt   *time.Time    `json:"finishedAt,omitempty"`
	Observations []Observation `json:"observations"`
	Probes       []Probe       `json:"probes"`
}

type PlaylistSummary struct {
	URL      string  `json:"url"`
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
	Bitrate  int     `json:"bitrate,omitempty"`
	Duration float64 `json:"durationSeconds,omitempty"`
}

type MediaSummary struct {
	MediaID string            `json:"mediaId"`
	Masters []PlaylistSummary `json:"masters"`
	Videos  []PlaylistSummary `json:"videos"`
	Audios  []PlaylistSummary `json:"audios"`
}

type Report struct {
	SessionID           string         `json:"sessionId"`
	StartedAt           time.Time      `json:"startedAt"`
	FinishedAt          time.Time      `json:"finishedAt"`
	MasterDetected      bool           `json:"masterDetected"`
	ObservationCount    int            `json:"observationCount"`
	UniquePlaylistCount int            `json:"uniquePlaylistCount"`
	FailedProbeCount    int            `json:"failedProbeCount"`
	Media               []MediaSummary `json:"media"`
}

type session struct {
	id           string
	status       string
	startedAt    time.Time
	finishedAt   *time.Time
	observations []Observation
	probes       map[string]Probe
}

type Store struct {
	mu             sync.RWMutex
	writeMu        sync.Mutex
	sessions       map[string]*session
	diagnosticsDir string
	client         *http.Client
}

func NewStore(diagnosticsDir string, client *http.Client) *Store {
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(request *http.Request, _ []*http.Request) error {
				_, err := validateObservation(Observation{URL: request.URL.String()})
				return err
			},
		}
	}
	return &Store{
		sessions:       make(map[string]*session),
		diagnosticsDir: diagnosticsDir,
		client:         client,
	}
}

func (store *Store) Create() (SessionSnapshot, error) {
	id, err := randomID()
	if err != nil {
		return SessionSnapshot{}, err
	}
	if err := os.MkdirAll(filepath.Join(store.diagnosticsDir, id, "playlists"), 0o700); err != nil {
		return SessionSnapshot{}, fmt.Errorf("create diagnostics directory: %w", err)
	}

	created := &session{
		id:        id,
		status:    "active",
		startedAt: time.Now().UTC(),
		probes:    make(map[string]Probe),
	}
	store.mu.Lock()
	store.sessions[id] = created
	store.mu.Unlock()
	return snapshot(created), nil
}

func (store *Store) Get(id string) (SessionSnapshot, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	current, ok := store.sessions[id]
	if !ok {
		return SessionSnapshot{}, os.ErrNotExist
	}
	return snapshot(current), nil
}

func (store *Store) AddObservations(ctx context.Context, id string, observations []Observation) (SessionSnapshot, error) {
	if len(observations) == 0 || len(observations) > maxObservationBatch {
		return SessionSnapshot{}, fmt.Errorf("observation batch must contain between 1 and %d items", maxObservationBatch)
	}

	validated := make([]Observation, 0, len(observations))
	for _, observation := range observations {
		cleaned, err := validateObservation(observation)
		if err != nil {
			return SessionSnapshot{}, err
		}
		validated = append(validated, cleaned)
	}

	store.mu.Lock()
	current, ok := store.sessions[id]
	if !ok {
		store.mu.Unlock()
		return SessionSnapshot{}, os.ErrNotExist
	}
	if current.status != "active" {
		store.mu.Unlock()
		return SessionSnapshot{}, errors.New("capture session is not active")
	}
	unique := make([]Observation, 0, len(validated))
	for _, observation := range validated {
		current.observations = append(current.observations, observation)
		if _, exists := current.probes[observation.URL]; !exists {
			current.probes[observation.URL] = Probe{URL: observation.URL, Classification: "pending"}
			unique = append(unique, observation)
		}
	}
	store.mu.Unlock()

	if err := store.appendObservations(id, validated); err != nil {
		return SessionSnapshot{}, err
	}

	results := make(chan Probe, len(unique))
	semaphore := make(chan struct{}, probeConcurrency)
	var waitGroup sync.WaitGroup
	for _, observation := range unique {
		waitGroup.Add(1)
		go func(playlistURL string) {
			defer waitGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			results <- store.probe(ctx, id, playlistURL)
		}(observation.URL)
	}
	waitGroup.Wait()
	close(results)
	for probe := range results {
		store.mu.Lock()
		current.probes[probe.URL] = probe
		store.mu.Unlock()
	}

	return store.Get(id)
}

func (store *Store) Finish(id string) (Report, error) {
	store.mu.Lock()
	current, ok := store.sessions[id]
	if !ok {
		store.mu.Unlock()
		return Report{}, os.ErrNotExist
	}
	if current.status == "active" {
		now := time.Now().UTC()
		current.status = "finished"
		current.finishedAt = &now
	}
	report := buildReport(current)
	store.mu.Unlock()

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return Report{}, fmt.Errorf("encode report: %w", err)
	}
	path := filepath.Join(store.diagnosticsDir, id, "report.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return Report{}, fmt.Errorf("write report: %w", err)
	}
	return report, nil
}

func validateObservation(observation Observation) (Observation, error) {
	parsed, err := url.Parse(observation.URL)
	if err != nil {
		return Observation{}, fmt.Errorf("parse playlist URL: %w", err)
	}
	if parsed.Scheme != "https" || parsed.Hostname() != "video.twimg.com" || parsed.Port() != "" {
		return Observation{}, errors.New("playlist URL must use https://video.twimg.com")
	}
	if parsed.User != nil || !strings.HasSuffix(strings.ToLower(parsed.Path), ".m3u8") {
		return Observation{}, errors.New("playlist URL must be an m3u8 without user information")
	}
	observation.URL = parsed.String()
	if observation.SeenAt.IsZero() {
		observation.SeenAt = time.Now().UTC()
	} else {
		observation.SeenAt = observation.SeenAt.UTC()
	}
	if len(observation.PageURL) > 2048 {
		return Observation{}, errors.New("page URL is too long")
	}
	if len(observation.RequestType) > 32 {
		return Observation{}, errors.New("request type is too long")
	}
	return observation, nil
}

func (store *Store) probe(ctx context.Context, sessionID, playlistURL string) Probe {
	probe := classifyURL(playlistURL)
	probe.URL = playlistURL
	probe.FetchedAt = time.Now().UTC()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, playlistURL, nil)
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	request.Header.Set("User-Agent", "X-Downloader-Helper/0.1")
	request.Header.Set("Referer", "https://x.com/")

	response, err := store.client.Do(request)
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	defer response.Body.Close()
	probe.StatusCode = response.StatusCode
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		probe.Error = fmt.Sprintf("unexpected HTTP status %d", response.StatusCode)
		return probe
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxPlaylistBytes+1))
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	if len(body) > maxPlaylistBytes {
		probe.Error = "playlist exceeds 2 MiB"
		return probe
	}

	content := string(body)
	probe.Classification = classifyContent(content, probe.Kind)
	probe.DurationSeconds = playlistDuration(content)
	probe.MasterVariantCount = strings.Count(content, "#EXT-X-STREAM-INF:")
	probe.MasterAudioRenditions = countAudioRenditions(content)

	hash := sha256.Sum256([]byte(playlistURL))
	filename := hex.EncodeToString(hash[:]) + ".m3u8"
	path := filepath.Join(store.diagnosticsDir, sessionID, "playlists", filename)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		probe.Error = fmt.Sprintf("store playlist: %v", err)
		return probe
	}
	probe.StoredFile = filepath.ToSlash(filepath.Join("playlists", filename))
	return probe
}

func classifyURL(value string) Probe {
	parsed, _ := url.Parse(value)
	path := parsed.Path
	probe := Probe{Kind: "unknown", Classification: "unknown"}
	if match := mediaIDPattern.FindStringSubmatch(path); match != nil {
		probe.MediaID = match[1]
	}
	if match := videoPattern.FindStringSubmatch(path); match != nil {
		probe.Kind = "video"
		probe.Width, _ = strconv.Atoi(match[1])
		probe.Height, _ = strconv.Atoi(match[2])
	} else if match := audioPattern.FindStringSubmatch(path); match != nil {
		probe.Kind = "audio"
		probe.Bitrate, _ = strconv.Atoi(match[1])
	}
	return probe
}

func classifyContent(content, pathKind string) string {
	if strings.Contains(content, "#EXT-X-STREAM-INF:") {
		return "master"
	}
	if strings.Contains(content, "#EXTINF:") || strings.Contains(content, "#EXT-X-MAP:") {
		if pathKind == "video" || pathKind == "audio" {
			return pathKind
		}
		return "media"
	}
	return "unknown"
}

func playlistDuration(content string) float64 {
	var duration float64
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "#EXTINF:") {
			continue
		}
		value := strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ",")
		if comma := strings.IndexByte(value, ','); comma >= 0 {
			value = value[:comma]
		}
		seconds, err := strconv.ParseFloat(value, 64)
		if err == nil {
			duration += seconds
		}
	}
	return duration
}

func countAudioRenditions(content string) int {
	count := 0
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#EXT-X-MEDIA:") && strings.Contains(line, "TYPE=AUDIO") {
			count++
		}
	}
	return count
}

func (store *Store) appendObservations(id string, observations []Observation) error {
	store.writeMu.Lock()
	defer store.writeMu.Unlock()
	path := filepath.Join(store.diagnosticsDir, id, "requests.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open observation log: %w", err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, observation := range observations {
		if err := encoder.Encode(observation); err != nil {
			return fmt.Errorf("write observation: %w", err)
		}
	}
	return nil
}

func snapshot(current *session) SessionSnapshot {
	result := SessionSnapshot{
		ID:           current.id,
		Status:       current.status,
		StartedAt:    current.startedAt,
		FinishedAt:   current.finishedAt,
		Observations: append([]Observation(nil), current.observations...),
		Probes:       make([]Probe, 0, len(current.probes)),
	}
	for _, probe := range current.probes {
		result.Probes = append(result.Probes, probe)
	}
	sort.Slice(result.Probes, func(i, j int) bool { return result.Probes[i].URL < result.Probes[j].URL })
	return result
}

func buildReport(current *session) Report {
	finishedAt := time.Now().UTC()
	if current.finishedAt != nil {
		finishedAt = *current.finishedAt
	}
	report := Report{
		SessionID:           current.id,
		StartedAt:           current.startedAt,
		FinishedAt:          finishedAt,
		ObservationCount:    len(current.observations),
		UniquePlaylistCount: len(current.probes),
	}

	grouped := make(map[string]*MediaSummary)
	for _, probe := range current.probes {
		if probe.Error != "" {
			report.FailedProbeCount++
		}
		if probe.Classification == "master" {
			report.MasterDetected = true
		}
		mediaID := probe.MediaID
		if mediaID == "" {
			mediaID = "unknown"
		}
		summary := grouped[mediaID]
		if summary == nil {
			summary = &MediaSummary{MediaID: mediaID}
			grouped[mediaID] = summary
		}
		item := PlaylistSummary{
			URL: probe.URL, Width: probe.Width, Height: probe.Height,
			Bitrate: probe.Bitrate, Duration: probe.DurationSeconds,
		}
		switch probe.Classification {
		case "master":
			summary.Masters = append(summary.Masters, item)
		case "video":
			summary.Videos = append(summary.Videos, item)
		case "audio":
			summary.Audios = append(summary.Audios, item)
		}
	}

	for _, summary := range grouped {
		sort.Slice(summary.Videos, func(i, j int) bool {
			return summary.Videos[i].Width*summary.Videos[i].Height > summary.Videos[j].Width*summary.Videos[j].Height
		})
		sort.Slice(summary.Audios, func(i, j int) bool { return summary.Audios[i].Bitrate > summary.Audios[j].Bitrate })
		report.Media = append(report.Media, *summary)
	}
	sort.Slice(report.Media, func(i, j int) bool { return report.Media[i].MediaID < report.Media[j].MediaID })
	return report
}

func randomID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	return hex.EncodeToString(buffer), nil
}

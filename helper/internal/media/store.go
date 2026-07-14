package media

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"x-downloader/helper/internal/hls"
	"x-downloader/helper/internal/statefile"
)

const maxMasterBytes = 2 << 20
const defaultMaxCandidates = 300
const fallbackUserAgent = "X-Downloader-Helper/0.5"

var numericID = regexp.MustCompile(`^\d+$`)

var ErrCandidateNotFound = errors.New("media candidate not found")

type Context struct {
	PageURL      string    `json:"pageUrl,omitempty"`
	PostURL      string    `json:"postUrl,omitempty"`
	PostID       string    `json:"postId,omitempty"`
	Author       string    `json:"author,omitempty"`
	CreatedAt    time.Time `json:"createdAt,omitempty"`
	MediaIndex   int       `json:"mediaIndex,omitempty"`
	ThumbnailURL string    `json:"thumbnailUrl,omitempty"`
}

type Candidate struct {
	ID           string        `json:"id"`
	MediaID      string        `json:"mediaId"`
	MasterURL    string        `json:"masterUrl"`
	UserAgent    string        `json:"userAgent,omitempty"`
	Variants     []hls.Variant `json:"variants"`
	Context      Context       `json:"context"`
	DiscoveredAt time.Time     `json:"discoveredAt"`
}

type Store struct {
	mu            sync.RWMutex
	persistMu     sync.Mutex
	candidates    map[string]Candidate
	dirty         map[string]struct{}
	client        *http.Client
	stateFile     string
	repository    Repository
	maxCandidates int
}

type Repository interface {
	LoadRecentCandidates(limit int) ([]Candidate, error)
	GetCandidate(id string) (Candidate, error)
	UpsertCandidates(candidates []Candidate) error
	CandidateCount() (int, error)
}

type persistedState struct {
	Version    int         `json:"version"`
	Candidates []Candidate `json:"candidates"`
}

func NewStore(client *http.Client) *Store {
	return newStore(client, "", defaultMaxCandidates)
}

func NewPersistentStore(stateFile string, maxCandidates int, client *http.Client) (*Store, error) {
	if maxCandidates <= 0 {
		maxCandidates = defaultMaxCandidates
	}
	store := newStore(client, stateFile, maxCandidates)
	var saved persistedState
	if err := statefile.Read(stateFile, &saved); err != nil {
		return nil, fmt.Errorf("load media state: %w", err)
	}
	if saved.Version != 0 && saved.Version != 1 {
		return nil, fmt.Errorf("unsupported media state version %d", saved.Version)
	}
	for _, candidate := range saved.Candidates {
		if candidate.ID == "" || candidate.MediaID == "" {
			continue
		}
		store.candidates[candidate.ID] = candidate
	}
	store.pruneLocked()
	return store, nil
}

func NewRepositoryStore(repository Repository, maxCandidates int, client *http.Client) (*Store, error) {
	if repository == nil {
		return nil, errors.New("candidate repository is required")
	}
	if maxCandidates <= 0 {
		maxCandidates = defaultMaxCandidates
	}
	store := newStore(client, "", maxCandidates)
	store.repository = repository
	items, err := repository.LoadRecentCandidates(maxCandidates)
	if err != nil {
		return nil, fmt.Errorf("load media state: %w", err)
	}
	for _, candidate := range items {
		if candidate.ID != "" && candidate.MediaID != "" {
			store.candidates[candidate.ID] = candidate
		}
	}
	store.pruneLocked()
	return store, nil
}

func newStore(client *http.Client, stateFile string, maxCandidates int) *Store {
	if client == nil {
		client = &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(request *http.Request, _ []*http.Request) error {
				_, err := hls.ValidatePlaylistURL(request.URL.String())
				return err
			},
		}
	}
	return &Store{
		candidates:    make(map[string]Candidate),
		dirty:         make(map[string]struct{}),
		client:        client,
		stateFile:     stateFile,
		maxCandidates: maxCandidates,
	}
}

func (store *Store) Register(ctx context.Context, masterURL, userAgent string, pageContext Context) (Candidate, error) {
	parsedURL, err := hls.ValidatePlaylistURL(masterURL)
	if err != nil {
		return Candidate{}, err
	}
	mediaID := hls.MediaID(parsedURL)
	if mediaID == "" {
		return Candidate{}, errors.New("master playlist URL does not contain a media ID")
	}
	cleanContext, err := validateContext(pageContext, mediaID)
	if err != nil {
		return Candidate{}, err
	}
	id := "media-" + mediaID
	userAgent, err = NormalizeUserAgent(userAgent)
	if err != nil {
		return Candidate{}, err
	}

	store.mu.Lock()
	if existing, ok := store.candidates[id]; ok && existing.MasterURL == parsedURL.String() {
		existing.Context = mergeContext(existing.Context, cleanContext)
		existing.UserAgent = userAgent
		store.candidates[id] = existing
		store.dirty[id] = struct{}{}
		store.mu.Unlock()
		if err := store.persist(); err != nil {
			return Candidate{}, err
		}
		slog.Debug("media candidate context updated", "candidateId", id, "mediaId", mediaID, "postId", existing.Context.PostID)
		return existing, nil
	}
	store.mu.Unlock()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return Candidate{}, err
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Referer", "https://x.com/")
	response, err := store.client.Do(request)
	if err != nil {
		return Candidate{}, fmt.Errorf("fetch master playlist: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Candidate{}, fmt.Errorf("fetch master playlist: HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxMasterBytes+1))
	if err != nil {
		return Candidate{}, fmt.Errorf("read master playlist: %w", err)
	}
	if len(body) > maxMasterBytes {
		return Candidate{}, errors.New("master playlist exceeds 2 MiB")
	}
	master, err := hls.ParseMaster(string(body), parsedURL.String())
	if err != nil {
		return Candidate{}, fmt.Errorf("parse master playlist: %w", err)
	}

	candidate := Candidate{
		ID:           id,
		MediaID:      mediaID,
		MasterURL:    parsedURL.String(),
		UserAgent:    userAgent,
		Variants:     master.Variants,
		Context:      cleanContext,
		DiscoveredAt: time.Now().UTC(),
	}
	store.mu.Lock()
	if existing, ok := store.candidates[id]; ok {
		candidate.Context = mergeContext(existing.Context, candidate.Context)
	}
	store.candidates[id] = candidate
	store.dirty[id] = struct{}{}
	store.pruneLocked()
	store.mu.Unlock()
	if err := store.persist(); err != nil {
		return Candidate{}, err
	}
	slog.Info("media candidate ready",
		"candidateId", candidate.ID,
		"mediaId", candidate.MediaID,
		"postId", candidate.Context.PostID,
		"mediaIndex", candidate.Context.MediaIndex,
		"variants", len(candidate.Variants),
	)
	return candidate, nil
}

func NormalizeUserAgent(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallbackUserAgent, nil
	}
	if len(value) > 512 || !utf8.ValidString(value) {
		return "", errors.New("browser user agent is invalid")
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return "", errors.New("browser user agent contains control characters")
		}
	}
	return value, nil
}

func (store *Store) UpdateUserAgent(id, value string) error {
	userAgent, err := NormalizeUserAgent(value)
	if err != nil {
		return err
	}
	store.mu.Lock()
	candidate, ok := store.candidates[id]
	if !ok {
		store.mu.Unlock()
		candidate, err = store.Get(id)
		if err != nil {
			return err
		}
		store.mu.Lock()
	}
	candidate.UserAgent = userAgent
	store.candidates[id] = candidate
	store.dirty[id] = struct{}{}
	store.mu.Unlock()
	return store.persist()
}

func (store *Store) Get(id string) (Candidate, error) {
	store.mu.RLock()
	candidate, ok := store.candidates[id]
	store.mu.RUnlock()
	if !ok {
		if store.repository == nil {
			return Candidate{}, ErrCandidateNotFound
		}
		loaded, err := store.repository.GetCandidate(id)
		if err != nil {
			return Candidate{}, err
		}
		store.mu.Lock()
		store.candidates[id] = loaded
		store.pruneLocked()
		store.mu.Unlock()
		return loaded, nil
	}
	return candidate, nil
}

func (store *Store) List() []Candidate {
	store.mu.RLock()
	defer store.mu.RUnlock()
	result := make([]Candidate, 0, len(store.candidates))
	for _, candidate := range store.candidates {
		result = append(result, candidate)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].DiscoveredAt.After(result[j].DiscoveredAt) })
	return result
}

func (store *Store) Count() int {
	if store.repository != nil {
		if count, err := store.repository.CandidateCount(); err == nil {
			return count
		}
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	return len(store.candidates)
}

func (store *Store) pruneLocked() {
	if store.maxCandidates <= 0 || len(store.candidates) <= store.maxCandidates {
		return
	}
	items := make([]Candidate, 0, len(store.candidates))
	for _, candidate := range store.candidates {
		items = append(items, candidate)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].DiscoveredAt.Before(items[j].DiscoveredAt) })
	for index := 0; index < len(items)-store.maxCandidates; index++ {
		delete(store.candidates, items[index].ID)
	}
}

func (store *Store) persist() error {
	if store.repository != nil {
		store.persistMu.Lock()
		defer store.persistMu.Unlock()
		// Keep the candidate lock until the write is acknowledged so an update
		// to the same candidate cannot have its dirty marker cleared by an older
		// in-flight snapshot.
		store.mu.Lock()
		defer store.mu.Unlock()
		items := make([]Candidate, 0, len(store.dirty))
		for id := range store.dirty {
			if candidate, ok := store.candidates[id]; ok {
				items = append(items, candidate)
			}
		}
		if len(items) == 0 {
			return nil
		}
		if err := store.repository.UpsertCandidates(items); err != nil {
			return fmt.Errorf("persist media state: %w", err)
		}
		for _, candidate := range items {
			delete(store.dirty, candidate.ID)
		}
		return nil
	}
	if store.stateFile == "" {
		return nil
	}
	store.persistMu.Lock()
	defer store.persistMu.Unlock()
	state := persistedState{Version: 1, Candidates: store.List()}
	if err := statefile.Write(store.stateFile, state); err != nil {
		return fmt.Errorf("persist media state: %w", err)
	}
	return nil
}

func validateContext(input Context, mediaID string) (Context, error) {
	if input.PostID != "" && !numericID.MatchString(input.PostID) {
		return Context{}, errors.New("post ID must be numeric")
	}
	if input.MediaIndex < 0 || input.MediaIndex > 20 {
		return Context{}, errors.New("media index is out of range")
	}
	if len(input.Author) > 64 {
		return Context{}, errors.New("author is too long")
	}
	if input.PageURL != "" && !isXPageURL(input.PageURL) {
		return Context{}, errors.New("page URL must be an X/Twitter URL")
	}
	if input.PostURL != "" && !isXPageURL(input.PostURL) {
		return Context{}, errors.New("post URL must be an X/Twitter URL")
	}
	if input.ThumbnailURL != "" {
		thumbnail, err := url.Parse(input.ThumbnailURL)
		if err != nil || thumbnail.Scheme != "https" || thumbnail.Hostname() != "pbs.twimg.com" {
			return Context{}, errors.New("thumbnail URL must use https://pbs.twimg.com")
		}
		if !strings.Contains(thumbnail.Path, mediaID) {
			return Context{}, errors.New("thumbnail URL does not match the media ID")
		}
	}
	input.Author = strings.TrimPrefix(strings.TrimSpace(input.Author), "@")
	return input, nil
}

func isXPageURL(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil {
		return false
	}
	switch parsed.Hostname() {
	case "x.com", "www.x.com", "mobile.x.com", "twitter.com", "www.twitter.com", "mobile.twitter.com":
		return true
	default:
		return false
	}
}

func mergeContext(current, update Context) Context {
	if update.PageURL != "" {
		current.PageURL = update.PageURL
	}
	if update.PostURL != "" {
		current.PostURL = update.PostURL
	}
	if update.PostID != "" {
		current.PostID = update.PostID
	}
	if update.Author != "" {
		current.Author = update.Author
	}
	if !update.CreatedAt.IsZero() {
		current.CreatedAt = update.CreatedAt
	}
	if update.MediaIndex > 0 {
		current.MediaIndex = update.MediaIndex
	}
	if update.ThumbnailURL != "" {
		current.ThumbnailURL = update.ThumbnailURL
	}
	return current
}

func CandidateID(masterURL, mediaID string) string {
	if mediaID != "" {
		return "media-" + mediaID
	}
	hash := sha256.Sum256([]byte(masterURL))
	return "media-" + hex.EncodeToString(hash[:8])
}

package settings

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"x-downloader/helper/internal/downloadpath"
	"x-downloader/helper/internal/folderpicker"
	"x-downloader/helper/internal/statefile"
)

const stateVersion = 2

var ErrSelectionCancelled = folderpicker.ErrCancelled

type RuntimeTarget interface {
	SetDownloadDir(string)
	SetFilenameTemplate(string)
	SetConcurrency(int)
	SetRetryCount(int)
}

type Defaults struct {
	DownloadDir      string
	FilenameTemplate string
	Concurrency      int
	RetryCount       int
}

type Values struct {
	DownloadDir      string `json:"downloadDir"`
	FilenameTemplate string `json:"filenameTemplate"`
	Concurrency      int    `json:"concurrency"`
	RetryCount       int    `json:"retryCount"`
}

type Snapshot struct {
	Values
	DefaultDownloadDir      string `json:"defaultDownloadDir"`
	DefaultFilenameTemplate string `json:"defaultFilenameTemplate"`
	DefaultConcurrency      int    `json:"defaultConcurrency"`
	DefaultRetryCount       int    `json:"defaultRetryCount"`
}

type Update struct {
	DownloadDir      *string
	FilenameTemplate *string
	Concurrency      *int
	RetryCount       *int
}

type persistedState struct {
	Version int `json:"version"`
	Values
}

type Repository interface {
	LoadSettings() (Values, bool, error)
	SaveSettings(Values) error
}

type Manager struct {
	mu         sync.RWMutex
	stateFile  string
	repository Repository
	values     Values
	defaults   Values
	target     RuntimeTarget
	picker     folderpicker.Picker
}

func New(stateFile string, defaults Defaults, picker folderpicker.Picker) (*Manager, error) {
	return newManager(stateFile, nil, defaults, picker)
}

func NewRepositoryManager(repository Repository, defaults Defaults, picker folderpicker.Picker) (*Manager, error) {
	if repository == nil {
		return nil, errors.New("settings repository is required")
	}
	return newManager("", repository, defaults, picker)
}

func newManager(stateFile string, repository Repository, defaults Defaults, picker folderpicker.Picker) (*Manager, error) {
	defaultValues, err := validateValues(Values{
		DownloadDir: defaults.DownloadDir, FilenameTemplate: defaults.FilenameTemplate,
		Concurrency: defaults.Concurrency, RetryCount: defaults.RetryCount,
	}, false)
	if err != nil {
		return nil, fmt.Errorf("validate default application settings: %w", err)
	}
	if picker == nil {
		picker = folderpicker.Native{}
	}
	manager := &Manager{
		stateFile:  stateFile,
		repository: repository,
		values:     defaultValues,
		defaults:   defaultValues,
		picker:     picker,
	}
	if repository != nil {
		saved, found, err := repository.LoadSettings()
		if err != nil {
			return nil, fmt.Errorf("load application settings: %w", err)
		}
		if found {
			manager.values = saved
		}
	} else {
		var saved persistedState
		if err := statefile.Read(stateFile, &saved); err != nil {
			return nil, fmt.Errorf("load application settings: %w", err)
		}
		if saved.Version != 0 && saved.Version != 1 && saved.Version != stateVersion {
			return nil, fmt.Errorf("unsupported application settings version %d", saved.Version)
		}
		if saved.DownloadDir != "" {
			manager.values.DownloadDir = saved.DownloadDir
		}
		if saved.Version >= 2 {
			manager.values.FilenameTemplate = saved.FilenameTemplate
			manager.values.Concurrency = saved.Concurrency
			manager.values.RetryCount = saved.RetryCount
		}
	}
	manager.values, err = validateValues(manager.values, false)
	if err != nil {
		return nil, fmt.Errorf("validate saved application settings: %w", err)
	}
	return manager, nil
}

func (manager *Manager) Bind(target RuntimeTarget) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.target = target
	manager.applyLocked()
}

func (manager *Manager) Get() Snapshot {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.snapshotLocked()
}

func (manager *Manager) Update(update Update) (Snapshot, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	next := manager.values
	prepareDirectory := false
	if update.DownloadDir != nil {
		next.DownloadDir = *update.DownloadDir
		prepareDirectory = true
	}
	if update.FilenameTemplate != nil {
		next.FilenameTemplate = *update.FilenameTemplate
	}
	if update.Concurrency != nil {
		next.Concurrency = *update.Concurrency
	}
	if update.RetryCount != nil {
		next.RetryCount = *update.RetryCount
	}
	validated, err := validateValues(next, prepareDirectory)
	if err != nil {
		return Snapshot{}, err
	}
	if err := manager.save(validated); err != nil {
		return Snapshot{}, fmt.Errorf("save application settings: %w", err)
	}
	manager.values = validated
	manager.applyLocked()
	return manager.snapshotLocked(), nil
}

func (manager *Manager) save(values Values) error {
	if manager.repository != nil {
		return manager.repository.SaveSettings(values)
	}
	return statefile.Write(manager.stateFile, persistedState{Version: stateVersion, Values: values})
}

func (manager *Manager) UpdateDownloadDir(path string) (Snapshot, error) {
	return manager.Update(Update{DownloadDir: &path})
}

func (manager *Manager) PickDownloadDirectory(ctx context.Context) (string, error) {
	path, err := manager.picker.Pick(ctx)
	if err != nil {
		if errors.Is(err, folderpicker.ErrCancelled) {
			return "", ErrSelectionCancelled
		}
		return "", err
	}
	path, err = downloadpath.Normalize(path)
	if err != nil {
		return "", fmt.Errorf("validate selected download directory: %w", err)
	}
	return filepath.Clean(path), nil
}

func (manager *Manager) applyLocked() {
	if manager.target == nil {
		return
	}
	manager.target.SetDownloadDir(manager.values.DownloadDir)
	manager.target.SetFilenameTemplate(manager.values.FilenameTemplate)
	manager.target.SetConcurrency(manager.values.Concurrency)
	manager.target.SetRetryCount(manager.values.RetryCount)
}

func (manager *Manager) snapshotLocked() Snapshot {
	return Snapshot{
		Values:             manager.values,
		DefaultDownloadDir: manager.defaults.DownloadDir, DefaultFilenameTemplate: manager.defaults.FilenameTemplate,
		DefaultConcurrency: manager.defaults.Concurrency, DefaultRetryCount: manager.defaults.RetryCount,
	}
}

func validateValues(values Values, prepareDirectory bool) (Values, error) {
	var err error
	if prepareDirectory {
		values.DownloadDir, err = downloadpath.Prepare(values.DownloadDir)
	} else {
		values.DownloadDir, err = downloadpath.Normalize(values.DownloadDir)
	}
	if err != nil {
		return Values{}, err
	}
	values.FilenameTemplate = strings.TrimSpace(values.FilenameTemplate)
	if values.FilenameTemplate == "" {
		return Values{}, errors.New("filename template must not be empty")
	}
	if len(values.FilenameTemplate) > 512 {
		return Values{}, errors.New("filename template must not exceed 512 bytes")
	}
	if strings.ContainsAny(values.FilenameTemplate, `/\\`) {
		return Values{}, errors.New("filename template must not contain path separators")
	}
	if values.Concurrency < 1 || values.Concurrency > 4 {
		return Values{}, errors.New("concurrency must be between 1 and 4")
	}
	if values.RetryCount < 0 || values.RetryCount > 5 {
		return Values{}, errors.New("retry count must be between 0 and 5")
	}
	return values, nil
}

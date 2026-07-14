package settings

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"x-downloader/helper/internal/downloadpath"
	"x-downloader/helper/internal/folderpicker"
	"x-downloader/helper/internal/statefile"
)

const stateVersion = 1

var ErrSelectionCancelled = folderpicker.ErrCancelled

type DownloadDirectoryTarget interface {
	SetDownloadDir(string)
}

type Snapshot struct {
	DownloadDir        string `json:"downloadDir"`
	DefaultDownloadDir string `json:"defaultDownloadDir"`
}

type persistedState struct {
	Version     int    `json:"version"`
	DownloadDir string `json:"downloadDir"`
}

type Manager struct {
	mu                 sync.RWMutex
	stateFile          string
	downloadDir        string
	defaultDownloadDir string
	target             DownloadDirectoryTarget
	picker             folderpicker.Picker
}

func New(stateFile, defaultDownloadDir string, picker folderpicker.Picker) (*Manager, error) {
	defaultDownloadDir, err := downloadpath.Normalize(defaultDownloadDir)
	if err != nil {
		return nil, fmt.Errorf("validate default download directory: %w", err)
	}
	if picker == nil {
		picker = folderpicker.Native{}
	}
	manager := &Manager{
		stateFile:          stateFile,
		downloadDir:        defaultDownloadDir,
		defaultDownloadDir: defaultDownloadDir,
		picker:             picker,
	}
	var saved persistedState
	if err := statefile.Read(stateFile, &saved); err != nil {
		return nil, fmt.Errorf("load application settings: %w", err)
	}
	if saved.Version != 0 && saved.Version != stateVersion {
		return nil, fmt.Errorf("unsupported application settings version %d", saved.Version)
	}
	if saved.DownloadDir != "" {
		downloadDir, err := downloadpath.Normalize(saved.DownloadDir)
		if err != nil {
			return nil, fmt.Errorf("validate saved download directory: %w", err)
		}
		manager.downloadDir = downloadDir
	}
	return manager, nil
}

func (manager *Manager) Bind(target DownloadDirectoryTarget) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.target = target
	if target != nil {
		target.SetDownloadDir(manager.downloadDir)
	}
}

func (manager *Manager) Get() Snapshot {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return Snapshot{
		DownloadDir:        manager.downloadDir,
		DefaultDownloadDir: manager.defaultDownloadDir,
	}
}

func (manager *Manager) UpdateDownloadDir(path string) (Snapshot, error) {
	downloadDir, err := downloadpath.Prepare(path)
	if err != nil {
		return Snapshot{}, err
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := statefile.Write(manager.stateFile, persistedState{Version: stateVersion, DownloadDir: downloadDir}); err != nil {
		return Snapshot{}, fmt.Errorf("save application settings: %w", err)
	}
	manager.downloadDir = downloadDir
	if manager.target != nil {
		manager.target.SetDownloadDir(downloadDir)
	}
	return Snapshot{DownloadDir: downloadDir, DefaultDownloadDir: manager.defaultDownloadDir}, nil
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

package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const defaultFilenameTemplate = "{date}_{author}_{postId}_{mediaIndex}_{mediaId}_{height}p.{ext}"

type Config struct {
	ListenAddress    string `json:"listenAddress"`
	DownloadDir      string `json:"downloadDir"`
	TempDir          string `json:"tempDir"`
	DiagnosticsDir   string `json:"diagnosticsDir"`
	TokenFile        string `json:"tokenFile"`
	FFmpegPath       string `json:"ffmpegPath"`
	Concurrency      int    `json:"concurrency"`
	FilenameTemplate string `json:"filenameTemplate"`
}

func Default() (Config, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("find home directory: %w", err)
	}

	downloadDir := filepath.Join(homeDir, "Downloads", "X-Media")
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Config{}, fmt.Errorf("find user config directory: %w", err)
	}
	appConfigDir := filepath.Join(configDir, "x-downloader")
	return Config{
		ListenAddress:    "127.0.0.1:17890",
		DownloadDir:      downloadDir,
		TempDir:          filepath.Join(downloadDir, ".partial"),
		DiagnosticsDir:   filepath.Join(appConfigDir, "diagnostics"),
		TokenFile:        filepath.Join(appConfigDir, "token"),
		FFmpegPath:       "ffmpeg",
		Concurrency:      1,
		FilenameTemplate: defaultFilenameTemplate,
	}, nil
}

func Load(path string) (Config, error) {
	cfg, err := Default()
	if err != nil {
		return Config{}, err
	}

	if path != "" {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return Config{}, fmt.Errorf("read configuration: %w", readErr)
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return Config{}, fmt.Errorf("decode configuration: %w", err)
		}
	}

	cfg.DownloadDir, err = expandHome(cfg.DownloadDir)
	if err != nil {
		return Config{}, fmt.Errorf("expand download directory: %w", err)
	}
	cfg.TempDir, err = expandHome(cfg.TempDir)
	if err != nil {
		return Config{}, fmt.Errorf("expand temporary directory: %w", err)
	}
	cfg.DiagnosticsDir, err = expandHome(cfg.DiagnosticsDir)
	if err != nil {
		return Config{}, fmt.Errorf("expand diagnostics directory: %w", err)
	}
	cfg.TokenFile, err = expandHome(cfg.TokenFile)
	if err != nil {
		return Config{}, fmt.Errorf("expand token file: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) Validate() error {
	host, _, err := net.SplitHostPort(cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("invalid listen address: %w", err)
	}
	if host != "127.0.0.1" {
		return errors.New("listen address must bind to 127.0.0.1")
	}
	if cfg.DownloadDir == "" || !filepath.IsAbs(cfg.DownloadDir) {
		return errors.New("download directory must be absolute")
	}
	if cfg.TempDir == "" || !filepath.IsAbs(cfg.TempDir) {
		return errors.New("temporary directory must be absolute")
	}
	if cfg.DiagnosticsDir == "" || !filepath.IsAbs(cfg.DiagnosticsDir) {
		return errors.New("diagnostics directory must be absolute")
	}
	if cfg.TokenFile == "" || !filepath.IsAbs(cfg.TokenFile) {
		return errors.New("token file must be absolute")
	}
	if cfg.Concurrency < 1 || cfg.Concurrency > 4 {
		return errors.New("concurrency must be between 1 and 4")
	}
	if strings.TrimSpace(cfg.FFmpegPath) == "" {
		return errors.New("FFmpeg path must not be empty")
	}
	if strings.TrimSpace(cfg.FilenameTemplate) == "" {
		return errors.New("filename template must not be empty")
	}
	if strings.ContainsAny(cfg.FilenameTemplate, `/\\`) {
		return errors.New("filename template must not contain path separators")
	}
	return nil
}

func expandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return homeDir, nil
		}
		return filepath.Join(homeDir, strings.TrimPrefix(path, "~/")), nil
	}
	return filepath.Clean(path), nil
}

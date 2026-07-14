package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"x-downloader/helper/internal/auth"
	"x-downloader/helper/internal/config"
	"x-downloader/helper/internal/downloadpath"
	"x-downloader/helper/internal/httpapi"
	"x-downloader/helper/internal/jobs"
	"x-downloader/helper/internal/media"
	"x-downloader/helper/internal/settings"
	"x-downloader/helper/internal/storage"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "", "path to the helper JSON configuration")
	printToken := flag.Bool("print-token", false, "print the browser pairing token and exit")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load configuration", "error", err)
		os.Exit(1)
	}
	token, err := auth.LoadOrCreate(cfg.TokenFile)
	if err != nil {
		slog.Error("load authentication token", "error", err)
		os.Exit(1)
	}
	if *printToken {
		fmt.Println(token)
		return
	}
	resolvedFFmpeg, ffmpegErr := exec.LookPath(cfg.FFmpegPath)
	if ffmpegErr != nil {
		slog.Warn("FFmpeg is not available; downloads will fail", "path", cfg.FFmpegPath, "error", ffmpegErr)
	} else {
		slog.Info("FFmpeg detected", "path", resolvedFFmpeg)
	}
	defaultSettings := settings.Defaults{
		DownloadDir: cfg.DownloadDir, FilenameTemplate: cfg.FilenameTemplate,
		Concurrency: cfg.Concurrency, RetryCount: cfg.RetryCount,
	}
	database, err := storage.Open(filepath.Join(cfg.StateDir, "x-downloader.sqlite3"), storage.LegacyPaths{
		Candidates: filepath.Join(cfg.StateDir, "candidates.json"),
		Jobs:       filepath.Join(cfg.StateDir, "jobs.json"),
		Settings:   filepath.Join(cfg.StateDir, "settings.json"),
		Defaults: settings.Values{
			DownloadDir: cfg.DownloadDir, FilenameTemplate: cfg.FilenameTemplate,
			Concurrency: cfg.Concurrency, RetryCount: cfg.RetryCount,
		},
	})
	if err != nil {
		slog.Error("initialize SQLite storage", "error", err)
		os.Exit(1)
	}
	defer database.Close()
	mediaStore, err := media.NewRepositoryStore(database, 300, nil)
	if err != nil {
		slog.Error("initialize media store", "error", err)
		os.Exit(1)
	}
	appSettings, err := settings.NewRepositoryManager(database, defaultSettings, nil)
	if err != nil {
		slog.Error("initialize application settings", "error", err)
		os.Exit(1)
	}
	activeSettings := appSettings.Get()
	activeDownloadDir := activeSettings.DownloadDir
	if _, err := downloadpath.Prepare(activeDownloadDir); err != nil {
		slog.Warn("download directory is currently unavailable; change it from extension settings", "path", activeDownloadDir, "error", err)
	}
	jobManager, err := jobs.NewRepositoryManager(
		activeSettings.Concurrency,
		activeDownloadDir,
		cfg.TempDir,
		activeSettings.FilenameTemplate,
		database,
		500,
		mediaStore,
		jobs.FFmpegRunner{Path: cfg.FFmpegPath},
	)
	if err != nil {
		slog.Error("initialize download manager", "error", err)
		os.Exit(1)
	}
	appSettings.Bind(jobManager)
	ffmpegPath := cfg.FFmpegPath
	if resolvedFFmpeg != "" {
		ffmpegPath = resolvedFFmpeg
	}

	server := &http.Server{
		Addr: cfg.ListenAddress,
		Handler: httpapi.New(version, token, mediaStore, jobManager, httpapi.Options{
			Settings: appSettings,
			Storage:  database,
			Readiness: httpapi.Readiness{
				FFmpegReady: ffmpegErr == nil, FFmpegPath: ffmpegPath,
				ProxyConfigured: proxyConfigured(), PersistenceEnabled: true,
			},
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	shutdownContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-shutdownContext.Done()
		slog.Info("shutdown requested")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			slog.Error("shutdown helper", "error", err)
		}
	}()

	slog.Info("starting helper",
		"version", version,
		"address", cfg.ListenAddress,
		"downloadDir", activeDownloadDir,
		"tempDir", cfg.TempDir,
		"stateDir", cfg.StateDir,
		"database", database.Path(),
		"concurrency", activeSettings.Concurrency,
		"retryCount", activeSettings.RetryCount,
		"ffmpegPath", cfg.FFmpegPath,
		"proxyConfigured", proxyConfigured(),
	)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("serve helper", "error", err)
		os.Exit(1)
	}
	slog.Info("helper stopped")
}

func proxyConfigured() bool {
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

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
	"x-downloader/helper/internal/capture"
	"x-downloader/helper/internal/config"
	"x-downloader/helper/internal/httpapi"
	"x-downloader/helper/internal/jobs"
	"x-downloader/helper/internal/media"
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
		slog.Warn("FFmpeg is not available; diagnostics will work but downloads will fail", "path", cfg.FFmpegPath, "error", ffmpegErr)
	} else {
		slog.Info("FFmpeg detected", "path", resolvedFFmpeg)
	}
	captureStore := capture.NewStore(cfg.DiagnosticsDir, nil)
	mediaStore, err := media.NewPersistentStore(filepath.Join(cfg.StateDir, "candidates.json"), 300, nil)
	if err != nil {
		slog.Error("initialize media store", "error", err)
		os.Exit(1)
	}
	jobManager, err := jobs.NewPersistentManager(
		cfg.Concurrency,
		cfg.DownloadDir,
		cfg.TempDir,
		cfg.FilenameTemplate,
		filepath.Join(cfg.StateDir, "jobs.json"),
		500,
		mediaStore,
		jobs.FFmpegRunner{Path: cfg.FFmpegPath},
	)
	if err != nil {
		slog.Error("initialize download manager", "error", err)
		os.Exit(1)
	}
	downloadDirWritable := directoryWritable(cfg.DownloadDir)
	ffmpegPath := cfg.FFmpegPath
	if resolvedFFmpeg != "" {
		ffmpegPath = resolvedFFmpeg
	}

	server := &http.Server{
		Addr: cfg.ListenAddress,
		Handler: httpapi.New(version, token, captureStore, mediaStore, jobManager, httpapi.Readiness{
			FFmpegReady: ffmpegErr == nil, FFmpegPath: ffmpegPath,
			DownloadDir: cfg.DownloadDir, DownloadDirWritable: downloadDirWritable,
			ProxyConfigured: proxyConfigured(), Concurrency: cfg.Concurrency, PersistenceEnabled: true,
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
		"downloadDir", cfg.DownloadDir,
		"tempDir", cfg.TempDir,
		"diagnosticsDir", cfg.DiagnosticsDir,
		"stateDir", cfg.StateDir,
		"concurrency", cfg.Concurrency,
		"ffmpegPath", cfg.FFmpegPath,
		"proxyConfigured", proxyConfigured(),
	)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("serve helper", "error", err)
		os.Exit(1)
	}
	slog.Info("helper stopped")
}

func directoryWritable(path string) bool {
	file, err := os.CreateTemp(path, ".write-check-*")
	if err != nil {
		return false
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return false
	}
	return os.Remove(name) == nil
}

func proxyConfigured() bool {
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

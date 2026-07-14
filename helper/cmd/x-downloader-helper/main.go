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
	mediaStore := media.NewStore(nil)
	jobManager, err := jobs.NewManager(
		cfg.Concurrency,
		cfg.DownloadDir,
		cfg.TempDir,
		cfg.FilenameTemplate,
		mediaStore,
		jobs.FFmpegRunner{Path: cfg.FFmpegPath},
	)
	if err != nil {
		slog.Error("initialize download manager", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           httpapi.New(version, token, captureStore, mediaStore, jobManager),
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

func proxyConfigured() bool {
	for _, name := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"x-downloader/helper/internal/jobs"
	"x-downloader/helper/internal/media"
)

const maxRequestBodyBytes = 64 << 10
const APIVersion = "1"

type healthResponse struct {
	Status     string `json:"status"`
	Version    string `json:"version"`
	APIVersion string `json:"apiVersion"`
}

type Readiness struct {
	FFmpegReady         bool   `json:"ffmpegReady"`
	FFmpegPath          string `json:"ffmpegPath"`
	DownloadDir         string `json:"downloadDir"`
	DownloadDirWritable bool   `json:"downloadDirWritable"`
	ProxyConfigured     bool   `json:"proxyConfigured"`
	Concurrency         int    `json:"concurrency"`
	PersistenceEnabled  bool   `json:"persistenceEnabled"`
}

type statusResponse struct {
	Status         string         `json:"status"`
	Version        string         `json:"version"`
	APIVersion     string         `json:"apiVersion"`
	Readiness      Readiness      `json:"readiness"`
	CandidateCount int            `json:"candidateCount"`
	Jobs           map[string]int `json:"jobs"`
	Issues         []string       `json:"issues"`
}

type candidateRequest struct {
	MasterURL string        `json:"masterUrl"`
	Context   media.Context `json:"context"`
}

type createJobRequest struct {
	CandidateID string `json:"candidateId"`
	VariantID   string `json:"variantId,omitempty"`
}

func New(version, token string, candidates *media.Store, jobManager *jobs.Manager, readinessValues ...Readiness) http.Handler {
	readiness := Readiness{}
	if len(readinessValues) > 0 {
		readiness = readinessValues[0]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Version: version, APIVersion: APIVersion})
	})
	mux.Handle("GET /v1/status", requireToken(token, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		issues := make([]string, 0, 2)
		if !readiness.FFmpegReady {
			issues = append(issues, "ffmpeg_unavailable")
		}
		if !readiness.DownloadDirWritable {
			issues = append(issues, "download_directory_not_writable")
		}
		status := "ready"
		if len(issues) > 0 {
			status = "degraded"
		}
		writeJSON(w, http.StatusOK, statusResponse{
			Status: status, Version: version, APIVersion: APIVersion, Readiness: readiness,
			CandidateCount: candidates.Count(), Jobs: jobManager.Stats(), Issues: issues,
		})
	})))
	mux.Handle("POST /v1/candidates", requireToken(token, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var input candidateRequest
		if err := decodeJSON(w, request, &input); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		candidate, err := candidates.Register(request.Context(), input.MasterURL, input.Context)
		if err != nil {
			slog.Warn("candidate registration failed", "postId", input.Context.PostID, "error", redactVideoURLs(err.Error()))
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, candidate)
	})))
	mux.Handle("GET /v1/candidates", requireToken(token, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, candidates.List())
	})))
	mux.Handle("GET /v1/candidates/{id}", requireToken(token, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		candidate, err := candidates.Get(request.PathValue("id"))
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, candidate)
	})))
	mux.Handle("POST /v1/jobs", requireToken(token, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var input createJobRequest
		if err := decodeJSON(w, request, &input); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		job, err := jobManager.Submit(input.CandidateID, input.VariantID)
		if err != nil {
			slog.Warn("download job rejected", "candidateId", input.CandidateID, "variantId", input.VariantID, "error", err)
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, job)
	})))
	mux.Handle("GET /v1/jobs", requireToken(token, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, jobManager.List())
	})))
	mux.Handle("GET /v1/jobs/{id}", requireToken(token, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		job, err := jobManager.Get(request.PathValue("id"))
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, job)
	})))
	mux.Handle("DELETE /v1/jobs/{id}", requireToken(token, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		job, err := jobManager.Cancel(request.PathValue("id"))
		if err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, job)
	})))
	mux.Handle("POST /v1/jobs/{id}/reveal", requireToken(token, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if err := jobManager.Reveal(request.PathValue("id")); err != nil {
			writeDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})))
	return securityHeaders(logRequests(mux))
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (writer *statusWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusWriter) Write(data []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(data)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		started := time.Now()
		writer := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(writer, request)
		status := writer.status
		if status == 0 {
			status = http.StatusOK
		}
		isPolling := request.Method == http.MethodGet
		if isPolling && status < http.StatusBadRequest {
			return
		}
		level := slog.LevelInfo
		if status >= http.StatusBadRequest {
			level = slog.LevelWarn
		}
		slog.Log(request.Context(), level, "helper API request",
			"method", request.Method,
			"path", request.URL.Path,
			"status", status,
			"duration", time.Since(started).Round(time.Millisecond),
		)
	})
}

func redactVideoURLs(message string) string {
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

func requireToken(expected string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		if provided == "" || len(provided) != len(expected) || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			writeError(w, http.StatusUnauthorized, errors.New("invalid bearer token"))
			return
		}
		next.ServeHTTP(w, request)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, request)
	})
}

func decodeJSON(w http.ResponseWriter, request *http.Request, destination any) error {
	request.Body = http.MaxBytesReader(w, request.Body, maxRequestBodyBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func writeDomainError(w http.ResponseWriter, err error) {
	if errors.Is(err, media.ErrCandidateNotFound) || errors.Is(err, jobs.ErrJobNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if errors.Is(err, jobs.ErrQueueFull) {
		writeError(w, http.StatusTooManyRequests, err)
		return
	}
	writeError(w, http.StatusBadRequest, err)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

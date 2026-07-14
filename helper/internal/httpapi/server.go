package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"x-downloader/helper/internal/capture"
	"x-downloader/helper/internal/jobs"
	"x-downloader/helper/internal/media"
)

const maxRequestBodyBytes = 64 << 10

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

type observationsRequest struct {
	Observations []capture.Observation `json:"observations"`
}

type candidateRequest struct {
	MasterURL string        `json:"masterUrl"`
	Context   media.Context `json:"context"`
}

type createJobRequest struct {
	CandidateID string `json:"candidateId"`
	VariantID   string `json:"variantId,omitempty"`
}

func New(version, token string, captures *capture.Store, candidates *media.Store, jobManager *jobs.Manager) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, healthResponse{Status: "ok", Version: version})
	})
	mux.Handle("POST /v1/capture-sessions", requireToken(token, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		session, err := captures.Create()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusCreated, session)
	})))
	mux.Handle("GET /v1/capture-sessions/{id}", requireToken(token, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		session, err := captures.Get(request.PathValue("id"))
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, session)
	})))
	mux.Handle("POST /v1/capture-sessions/{id}/observations", requireToken(token, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var input observationsRequest
		if err := decodeJSON(w, request, &input); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		session, err := captures.AddObservations(request.Context(), request.PathValue("id"), input.Observations)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, session)
	})))
	mux.Handle("POST /v1/capture-sessions/{id}/finish", requireToken(token, http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		report, err := captures.Finish(request.PathValue("id"))
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, report)
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
		isObservationBatch := strings.HasSuffix(request.URL.Path, "/observations")
		if (isPolling || isObservationBatch) && status < http.StatusBadRequest {
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

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusNotFound, errors.New("capture session not found"))
		return
	}
	writeError(w, http.StatusBadRequest, err)
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

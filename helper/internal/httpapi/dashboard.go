package httpapi

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"x-downloader/helper/internal/storage"
)

//go:embed web/*
var dashboardAssets embed.FS

func registerDashboardRoutes(mux *http.ServeMux) {
	assets, _ := fs.Sub(dashboardAssets, "web")
	fileServer := http.FileServer(http.FS(assets))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(w, request)
			return
		}
		index, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			http.Error(w, "dashboard is unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
	mux.Handle("GET /dashboard.css", fileServer)
	mux.Handle("GET /dashboard.js", fileServer)
}

func registerStorageRoutes(mux *http.ServeMux, token string, database *storage.Database) {
	authenticate := func(handler http.HandlerFunc) http.Handler {
		return requireToken(token, handler)
	}
	mux.Handle("GET /v1/history", authenticate(func(w http.ResponseWriter, request *http.Request) {
		query, err := storage.ParseHistoryQuery(request.URL.Query())
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		page, err := database.SearchHistory(request.Context(), query)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	}))
	mux.Handle("PATCH /v1/history/{id}", authenticate(func(w http.ResponseWriter, request *http.Request) {
		id, err := pathInt64(request, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		var input struct {
			Note string `json:"note"`
		}
		if err := decodeJSON(w, request, &input); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := database.UpdateHistoryNote(request.Context(), id, input.Note); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	mux.Handle("DELETE /v1/history/{id}", authenticate(func(w http.ResponseWriter, request *http.Request) {
		id, err := pathInt64(request, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := database.DeleteHistoryItem(request.Context(), id); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	mux.Handle("GET /v1/statistics", authenticate(func(w http.ResponseWriter, request *http.Request) {
		result, err := database.Statistics(request.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}))
	mux.Handle("GET /v1/job-history", authenticate(func(w http.ResponseWriter, request *http.Request) {
		query, err := storage.ParseJobQuery(request.URL.Query())
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		page, err := database.SearchJobs(request.Context(), query)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	}))
	mux.Handle("GET /v1/tags", authenticate(func(w http.ResponseWriter, request *http.Request) {
		tags, err := database.ListTags(request.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, tags)
	}))
	mux.Handle("POST /v1/tags", authenticate(func(w http.ResponseWriter, request *http.Request) {
		name, color, err := decodeTagRequest(w, request)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		tag, err := database.CreateTag(request.Context(), name, color)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, tag)
	}))
	mux.Handle("PATCH /v1/tags/{id}", authenticate(func(w http.ResponseWriter, request *http.Request) {
		id, err := pathInt64(request, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		name, color, err := decodeTagRequest(w, request)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		tag, err := database.UpdateTag(request.Context(), id, name, color)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, tag)
	}))
	mux.Handle("DELETE /v1/tags/{id}", authenticate(func(w http.ResponseWriter, request *http.Request) {
		id, err := pathInt64(request, "id")
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := database.DeleteTag(request.Context(), id); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		pattern := method + " /v1/history/{id}/tags/{tagId}"
		mux.Handle(pattern, authenticate(func(w http.ResponseWriter, request *http.Request) {
			historyID, err := pathInt64(request, "id")
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			tagID, err := pathInt64(request, "tagId")
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			if request.Method == http.MethodPut {
				err = database.AssignTag(request.Context(), historyID, tagID)
			} else {
				err = database.RemoveTag(request.Context(), historyID, tagID)
			}
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		}))
	}
}

func pathInt64(request *http.Request, name string) (int64, error) {
	value := strings.TrimSpace(request.PathValue(name))
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, errors.New(name + " must be a positive integer")
	}
	return parsed, nil
}

func decodeTagRequest(w http.ResponseWriter, request *http.Request) (string, string, error) {
	var input struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := decodeJSON(w, request, &input); err != nil {
		return "", "", err
	}
	return input.Name, input.Color, nil
}

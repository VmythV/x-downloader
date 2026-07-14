package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"x-downloader/helper/internal/folderpicker"
	"x-downloader/helper/internal/jobs"
	"x-downloader/helper/internal/media"
	"x-downloader/helper/internal/settings"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type testRunner struct{}

func (testRunner) Run(_ context.Context, spec jobs.DownloadSpec, onProgress func(jobs.Progress)) error {
	onProgress(jobs.Progress{OutTimeSeconds: 4, Speed: "8x"})
	return os.WriteFile(spec.OutputPath, []byte("mp4"), 0o600)
}

type testEnvironment struct {
	handler     http.Handler
	root        string
	pickedDir   string
	appSettings *settings.Manager
}

func newTestEnvironment(t *testing.T) testEnvironment {
	t.Helper()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := "#EXTM3U\n#EXTINF:4,\nsegment.m4s\n"
		if strings.Contains(request.URL.Path, "master.m3u8") {
			body = `#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio-128000",URI="/amplify_video/2076268346560196608/pl/mp4a/128000/audio.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=1800000,RESOLUTION=1280x720,AUDIO="audio-128000"
/amplify_video/2076268346560196608/pl/avc1/1280x720/video.m3u8
`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}
	mediaStore := media.NewStore(client)
	root := t.TempDir()
	jobManager, err := jobs.NewManager(
		1,
		filepath.Join(root, "downloads"),
		filepath.Join(root, "partial"),
		"{postId}_{mediaId}_{height}p.{ext}",
		mediaStore,
		testRunner{},
	)
	if err != nil {
		t.Fatal(err)
	}
	pickedDir := filepath.Join(root, "picked-downloads")
	appSettings, err := settings.New(
		filepath.Join(root, "state", "settings.json"),
		filepath.Join(root, "downloads"),
		folderpicker.PickerFunc(func(context.Context) (string, error) { return pickedDir, nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	appSettings.Bind(jobManager)
	return testEnvironment{
		handler: New("test-version", "test-secret-token-value-1234567890", mediaStore, jobManager, Options{
			Settings: appSettings,
			Readiness: Readiness{
				FFmpegReady: true, FFmpegPath: "ffmpeg", Concurrency: 1, PersistenceEnabled: true,
			},
		}),
		root: root, pickedDir: pickedDir, appSettings: appSettings,
	}
}

func newTestHandler(t *testing.T) http.Handler {
	return newTestEnvironment(t).handler
}

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	response := httptest.NewRecorder()
	newTestHandler(t).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"version":"test-version"`) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestStatusReportsAuthenticatedReadiness(t *testing.T) {
	handler := newTestHandler(t)
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected unauthenticated status: %d", unauthorized.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	request.Header.Set("Authorization", "Bearer test-secret-token-value-1234567890")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"apiVersion":"2"`) {
		t.Fatalf("unexpected status response: %d %s", response.Code, response.Body.String())
	}
}

func TestSettingsPickUpdateAndRestoreDownloadDirectory(t *testing.T) {
	environment := newTestEnvironment(t)
	token := "Bearer test-secret-token-value-1234567890"

	pickRequest := httptest.NewRequest(http.MethodPost, "/v1/settings/pick-download-directory", nil)
	pickRequest.Header.Set("Authorization", token)
	pickResponse := httptest.NewRecorder()
	environment.handler.ServeHTTP(pickResponse, pickRequest)
	if pickResponse.Code != http.StatusOK || !strings.Contains(pickResponse.Body.String(), environment.pickedDir) {
		t.Fatalf("unexpected picker response: %d %s", pickResponse.Code, pickResponse.Body.String())
	}

	updateBody := `{"downloadDir":` + strconv.Quote(environment.pickedDir) + `}`
	updateRequest := httptest.NewRequest(http.MethodPut, "/v1/settings", strings.NewReader(updateBody))
	updateRequest.Header.Set("Authorization", token)
	updateResponse := httptest.NewRecorder()
	environment.handler.ServeHTTP(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusOK || environment.appSettings.Get().DownloadDir != environment.pickedDir {
		t.Fatalf("unexpected update response: %d %s", updateResponse.Code, updateResponse.Body.String())
	}

	restored, err := settings.New(filepath.Join(environment.root, "state", "settings.json"), filepath.Join(environment.root, "downloads"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Get().DownloadDir != environment.pickedDir {
		t.Fatalf("updated directory was not restored: %+v", restored.Get())
	}
}

func TestCandidateDownloadAPIFlow(t *testing.T) {
	handler := newTestHandler(t)
	token := "Bearer test-secret-token-value-1234567890"
	masterURL := "https://video.twimg.com/amplify_video/2076268346560196608/pl/master.m3u8"
	body := `{"masterUrl":"` + masterURL + `","context":{"postUrl":"https://x.com/test/status/123456","postId":"123456","author":"test","mediaIndex":1}}`
	createCandidate := httptest.NewRequest(http.MethodPost, "/v1/candidates", strings.NewReader(body))
	createCandidate.Header.Set("Authorization", token)
	candidateResponse := httptest.NewRecorder()
	handler.ServeHTTP(candidateResponse, createCandidate)
	if candidateResponse.Code != http.StatusOK {
		t.Fatalf("unexpected candidate response: %d %s", candidateResponse.Code, candidateResponse.Body.String())
	}
	var candidate media.Candidate
	if err := json.Unmarshal(candidateResponse.Body.Bytes(), &candidate); err != nil || len(candidate.Variants) != 1 {
		t.Fatalf("decode candidate: %v, body=%s", err, candidateResponse.Body.String())
	}

	jobBody := `{"candidateId":"` + candidate.ID + `","variantId":"` + candidate.Variants[0].ID + `"}`
	createJob := httptest.NewRequest(http.MethodPost, "/v1/jobs", strings.NewReader(jobBody))
	createJob.Header.Set("Authorization", token)
	jobResponse := httptest.NewRecorder()
	handler.ServeHTTP(jobResponse, createJob)
	if jobResponse.Code != http.StatusAccepted {
		t.Fatalf("unexpected job response: %d %s", jobResponse.Code, jobResponse.Body.String())
	}
	var job jobs.Job
	if err := json.Unmarshal(jobResponse.Body.Bytes(), &job); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		getJob := httptest.NewRequest(http.MethodGet, "/v1/jobs/"+job.ID, nil)
		getJob.Header.Set("Authorization", token)
		getResponse := httptest.NewRecorder()
		handler.ServeHTTP(getResponse, getJob)
		if err := json.Unmarshal(getResponse.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		if job.Status == "completed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job.Status != "completed" || filepath.Base(job.OutputPath) != "123456_2076268346560196608_720p.mp4" {
		t.Fatalf("unexpected completed job: %+v", job)
	}
}

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"x-downloader/helper/internal/capture"
	"x-downloader/helper/internal/jobs"
	"x-downloader/helper/internal/media"
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

func newTestHandler(t *testing.T) http.Handler {
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
	return New("test-version", "test-secret-token-value-1234567890", capture.NewStore(filepath.Join(root, "diagnostics"), client), mediaStore, jobManager)
}

func TestHealth(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	response := httptest.NewRecorder()
	newTestHandler(t).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"version":"test-version"`) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestCaptureEndpointsRequireToken(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/capture-sessions", nil)
	response := httptest.NewRecorder()
	newTestHandler(t).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d", response.Code)
	}
}

func TestCreatesCaptureSessionWithToken(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/v1/capture-sessions", nil)
	request.Header.Set("Authorization", "Bearer test-secret-token-value-1234567890")
	response := httptest.NewRecorder()
	newTestHandler(t).ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !bytes.Contains(response.Body.Bytes(), []byte(`"status":"active"`)) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
}

func TestCaptureSessionAPIFlow(t *testing.T) {
	handler := newTestHandler(t)
	token := "Bearer test-secret-token-value-1234567890"

	createRequest := httptest.NewRequest(http.MethodPost, "/v1/capture-sessions", nil)
	createRequest.Header.Set("Authorization", token)
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, createRequest)
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil || created.ID == "" {
		t.Fatalf("decode created session: %v, body=%s", err, createResponse.Body.String())
	}

	body := `{"observations":[{"url":"https://video.twimg.com/amplify_video/2076268346560196608/pl/mp4a/128000/audio.m3u8"}]}`
	observeRequest := httptest.NewRequest(http.MethodPost, "/v1/capture-sessions/"+created.ID+"/observations", strings.NewReader(body))
	observeRequest.Header.Set("Authorization", token)
	observeResponse := httptest.NewRecorder()
	handler.ServeHTTP(observeResponse, observeRequest)
	if observeResponse.Code != http.StatusOK {
		t.Fatalf("unexpected observation response: %d %s", observeResponse.Code, observeResponse.Body.String())
	}

	finishRequest := httptest.NewRequest(http.MethodPost, "/v1/capture-sessions/"+created.ID+"/finish", nil)
	finishRequest.Header.Set("Authorization", token)
	finishResponse := httptest.NewRecorder()
	handler.ServeHTTP(finishResponse, finishRequest)
	if finishResponse.Code != http.StatusOK || !bytes.Contains(finishResponse.Body.Bytes(), []byte(`"mediaId":"2076268346560196608"`)) {
		t.Fatalf("unexpected finish response: %d %s", finishResponse.Code, finishResponse.Body.String())
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

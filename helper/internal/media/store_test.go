package media

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestPersistentStoreRestoresCandidates(t *testing.T) {
	master := `#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",URI="/amplify_video/123/pl/mp4a/128000/audio.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=1000,RESOLUTION=1280x720,AUDIO="audio"
/amplify_video/123/pl/avc1/1280x720/video.m3u8
`
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(master)), Header: make(http.Header)}, nil
	})}
	statePath := filepath.Join(t.TempDir(), "media.json")
	store, err := NewPersistentStore(statePath, 10, client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Register(context.Background(), "https://video.twimg.com/amplify_video/123/pl/master.m3u8", Context{}); err != nil {
		t.Fatal(err)
	}
	restored, err := NewPersistentStore(statePath, 10, client)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Count() != 1 {
		t.Fatalf("expected one restored candidate, got %d", restored.Count())
	}
}

func TestRegisterBuildsCandidateAndMergesContext(t *testing.T) {
	master := `#EXTM3U
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio-128000",URI="/amplify_video/2075181378543779840/pl/mp4a/128000/audio.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=1000,RESOLUTION=1280x720,AUDIO="audio-128000"
/amplify_video/2075181378543779840/pl/avc1/1280x720/video.m3u8
`
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(master)), Header: make(http.Header)}, nil
	})}
	store := NewStore(client)
	url := "https://video.twimg.com/amplify_video/2075181378543779840/pl/master.m3u8"
	candidate, err := store.Register(context.Background(), url, Context{PageURL: "https://x.com/home"})
	if err != nil {
		t.Fatal(err)
	}
	if candidate.MediaID != "2075181378543779840" || candidate.Variants[0].Audio == nil {
		t.Fatalf("unexpected candidate: %+v", candidate)
	}
	updated, err := store.Register(context.Background(), url, Context{PostID: "12345", PostURL: "https://x.com/user/status/12345"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Context.PageURL == "" || updated.Context.PostID != "12345" {
		t.Fatalf("context was not merged: %+v", updated.Context)
	}
}

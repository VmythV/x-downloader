package capture

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestCaptureSessionProbesAndReportsMedia(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body := "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4.0,\nsegment.m4s\n"
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}

	store := NewStore(t.TempDir(), client)
	session, err := store.Create()
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AddObservations(context.Background(), session.ID, []Observation{{
		URL: "https://video.twimg.com/amplify_video/2076268346560196608/pl/avc1/1280x720/video.m3u8",
	}})
	if err != nil {
		t.Fatal(err)
	}
	report, err := store.Finish(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.UniquePlaylistCount != 1 || len(report.Media) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(report.Media[0].Videos) != 1 || report.Media[0].Videos[0].Height != 720 {
		t.Fatalf("unexpected media summary: %+v", report.Media[0])
	}
}

func TestRejectsArbitraryPlaylistHosts(t *testing.T) {
	_, err := validateObservation(Observation{URL: "https://example.com/video.m3u8"})
	if err == nil {
		t.Fatal("expected arbitrary host to be rejected")
	}
}

func TestClassifiesMasterPlaylist(t *testing.T) {
	content := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000,RESOLUTION=1280x720\nvideo.m3u8\n"
	if got := classifyContent(content, "unknown"); got != "master" {
		t.Fatalf("unexpected classification: %s", got)
	}
}

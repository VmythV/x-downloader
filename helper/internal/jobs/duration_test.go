package jobs

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type durationRoundTripFunc func(*http.Request) (*http.Response, error)

func (function durationRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestProbeHLSDuration(t *testing.T) {
	client := &http.Client{Transport: durationRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("User-Agent") != "Test Browser" {
			t.Fatalf("unexpected user agent: %q", request.Header.Get("User-Agent"))
		}
		body := "#EXTM3U\n#EXTINF:2.5,\na.m4s\n#EXTINF:3.75,\nb.m4s\n#EXT-X-ENDLIST\n"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	duration, err := probeHLSDuration(context.Background(), client, "https://video.twimg.com/amplify_video/123/video.m3u8", "Test Browser")
	if err != nil {
		t.Fatal(err)
	}
	if duration != 6.25 {
		t.Fatalf("duration = %v, want 6.25", duration)
	}
}

func TestProbeHLSDurationRejectsLivePlaylist(t *testing.T) {
	client := &http.Client{Transport: durationRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("#EXTM3U\n#EXTINF:2,\na.m4s\n")), Header: make(http.Header)}, nil
	})}
	if _, err := probeHLSDuration(context.Background(), client, "https://video.twimg.com/ext_tw_video/123/video.m3u8", ""); err == nil {
		t.Fatal("expected live playlist duration to be unavailable")
	}
}

func TestParseProgressCalculatesPercentage(t *testing.T) {
	var received Progress
	parseProgress(strings.NewReader("out_time_us=2500000\nspeed=3.5x\nprogress=continue\n"), 10, func(progress Progress) {
		received = progress
	})
	if received.OutTimeSeconds != 2.5 || received.Percent != 25 || received.Speed != "3.5x" || received.Phase != "downloading" {
		t.Fatalf("unexpected progress: %#v", received)
	}
}

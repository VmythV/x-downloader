package jobs

import (
	"slices"
	"testing"
)

func TestFFmpegArgumentsApplyBrowserUserAgentToBothInputs(t *testing.T) {
	userAgent := "Mozilla/5.0 TestBrowser/1"
	arguments := ffmpegArguments(DownloadSpec{
		VideoURL:  "https://video.twimg.com/video.m3u8",
		AudioURL:  "https://video.twimg.com/audio.m3u8",
		UserAgent: userAgent, OutputPath: "/tmp/output.mp4",
	})
	wantSequence := []string{
		"-user_agent", userAgent, "-i", "https://video.twimg.com/video.m3u8",
		"-user_agent", userAgent, "-i", "https://video.twimg.com/audio.m3u8",
	}
	start := slices.Index(arguments, "-user_agent")
	if start < 0 || !slices.Equal(arguments[start:start+len(wantSequence)], wantSequence) {
		t.Fatalf("user agent was not applied before both inputs: %v", arguments)
	}
}

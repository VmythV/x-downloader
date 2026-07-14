package jobs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"x-downloader/helper/internal/hls"
)

const maxMediaPlaylistBytes = 4 << 20

func probeHLSDuration(ctx context.Context, client *http.Client, playlistURL, userAgent string) (float64, error) {
	parsed, err := hls.ValidatePlaylistURL(playlistURL)
	if err != nil {
		return 0, err
	}
	if client == nil {
		client = &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(request *http.Request, _ []*http.Request) error {
				_, err := hls.ValidatePlaylistURL(request.URL.String())
				return err
			},
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return 0, err
	}
	if userAgent != "" {
		request.Header.Set("User-Agent", userAgent)
	}
	request.Header.Set("Referer", "https://x.com/")
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, fmt.Errorf("duration playlist returned HTTP %d", response.StatusCode)
	}
	reader := io.LimitReader(response.Body, maxMediaPlaylistBytes+1)
	scanner := bufio.NewScanner(reader)
	duration := 0.0
	hasEndList := false
	bytesRead := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		bytesRead += len(line) + 1
		if bytesRead > maxMediaPlaylistBytes {
			return 0, errors.New("duration playlist exceeds 4 MiB")
		}
		if line == "#EXT-X-ENDLIST" {
			hasEndList = true
			continue
		}
		if !strings.HasPrefix(line, "#EXTINF:") {
			continue
		}
		value := strings.TrimPrefix(line, "#EXTINF:")
		value, _, _ = strings.Cut(value, ",")
		seconds, parseErr := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if parseErr == nil && seconds > 0 {
			duration += seconds
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if !hasEndList || duration <= 0 {
		return 0, errors.New("duration is unavailable for a non-VOD playlist")
	}
	return duration, nil
}

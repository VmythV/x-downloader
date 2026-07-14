package jobs

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type FFmpegRunner struct {
	Path   string
	Client *http.Client
}

func (runner FFmpegRunner) Run(ctx context.Context, spec DownloadSpec, onProgress func(Progress)) error {
	duration, durationErr := probeHLSDuration(ctx, runner.Client, spec.VideoURL, spec.UserAgent)
	if durationErr != nil {
		slog.Debug("probe HLS duration", "error", durationErr)
	}
	if onProgress != nil {
		onProgress(Progress{DurationSeconds: duration, Phase: "preparing"})
	}
	command := exec.Command(runner.Path, ffmpegArguments(spec)...)
	prepareProcess(command)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open FFmpeg progress pipe: %w", err)
	}
	stderr := &limitedBuffer{limit: 64 << 10}
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start FFmpeg: %w", err)
	}

	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		parseProgress(stdout, duration, onProgress)
	}()
	waitDone := make(chan error, 1)
	go func() { waitDone <- command.Wait() }()

	select {
	case err := <-waitDone:
		<-progressDone
		if err != nil {
			return fmt.Errorf("FFmpeg failed: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return nil
	case <-ctx.Done():
		signalProcess(command)
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-waitDone:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			killProcess(command)
			<-waitDone
		}
		<-progressDone
		return context.Canceled
	}
}

func ffmpegArguments(spec DownloadSpec) []string {
	args := []string{
		"-hide_banner", "-nostdin", "-loglevel", "warning",
		"-progress", "pipe:1", "-nostats",
	}
	for _, input := range []string{spec.VideoURL, spec.AudioURL} {
		if spec.UserAgent != "" {
			args = append(args, "-user_agent", spec.UserAgent)
		}
		args = append(args, "-i", input)
	}
	return append(args,
		"-map", "0:v:0", "-map", "1:a:0",
		"-c", "copy", "-movflags", "+faststart",
		"-y", spec.OutputPath,
	)
}

func parseProgress(reader io.Reader, duration float64, onProgress func(Progress)) {
	if onProgress == nil {
		return
	}
	scanner := bufio.NewScanner(reader)
	current := Progress{DurationSeconds: duration, Phase: "downloading"}
	for scanner.Scan() {
		line := scanner.Text()
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "out_time_us":
			microseconds, _ := strconv.ParseFloat(value, 64)
			current.OutTimeSeconds = microseconds / 1_000_000
		case "speed":
			current.Speed = value
		case "progress":
			if duration > 0 {
				current.Percent = math.Min(99, math.Max(0, current.OutTimeSeconds/duration*100))
			}
			onProgress(current)
		}
	}
}

type limitedBuffer struct {
	mu    sync.Mutex
	data  bytes.Buffer
	limit int
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	originalLength := len(data)
	remaining := buffer.limit - buffer.data.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = buffer.data.Write(data)
	}
	return originalLength, nil
}

func (buffer *limitedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.data.String()
}

var _ io.Writer = (*limitedBuffer)(nil)

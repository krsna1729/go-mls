package stream

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// FFmpegInfo contains information about the FFmpeg installation
type FFmpegInfo struct {
	Available      bool   `json:"available"`
	Version        string `json:"version"`
	BuildDate      string `json:"build_date"`
	Configuration  string `json:"configuration"`
	Copyright      string `json:"copyright"`
	Path           string `json:"path"`
	Error          string `json:"error,omitempty"`
}

// DefaultArgs contains the default FFmpeg arguments used by different components
type DefaultArgs struct {
	Input     []string `json:"input"`
	Output    []string `json:"output"`
	Recording []string `json:"recording"`
	HLS       []string `json:"hls"`
}

// CheckFFmpegAvailability checks if FFmpeg is available and returns version info
func CheckFFmpegAvailability(ffmpegPath string) *FFmpegInfo {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}

	info := &FFmpegInfo{
		Available: false,
		Path:      ffmpegPath,
	}

	// Check if ffmpeg exists in PATH or at specified path
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffmpegPath, "-version")
	output, err := cmd.Output()
	if err != nil {
		info.Error = fmt.Sprintf("FFmpeg not found or failed to execute: %v", err)
		return info
	}

	info.Available = true
	
	// Parse version info from output
	outputStr := string(output)
	lines := strings.Split(outputStr, "\n")
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		
		// Extract version and copyright from first line
		if strings.HasPrefix(line, "ffmpeg version") {
			info.Version = extractVersion(line)
			// Extract copyright from the same line (after "Copyright")
			if copyrightIdx := strings.Index(line, "Copyright"); copyrightIdx != -1 {
				info.Copyright = strings.TrimSpace(line[copyrightIdx:])
			}
		}
		
		// Extract build date
		if strings.Contains(line, "built with") {
			info.BuildDate = extractBuildDate(line)
		}
		
		// Extract configuration (usually starts with "configuration:")
		if strings.HasPrefix(line, "configuration:") {
			info.Configuration = strings.TrimPrefix(line, "configuration:")
			info.Configuration = strings.TrimSpace(info.Configuration)
		}
	}

	return info
}

// extractVersion extracts version string from the version line
func extractVersion(line string) string {
	// Example: "ffmpeg version 4.4.2-0ubuntu0.22.04.1 Copyright..."
	parts := strings.Fields(line)
	if len(parts) >= 3 {
		return parts[2]
	}
	return "unknown"
}

// extractBuildDate extracts build date from line containing "built with"
func extractBuildDate(line string) string {
	// Example: "built with gcc 13 (Ubuntu 13.2.0-23ubuntu3)"
	if strings.Contains(line, "built with") {
		return strings.TrimSpace(line)
	}
	return "unknown"
}

// GetDefaultFFmpegArgs returns the default arguments used by different components
func GetDefaultFFmpegArgs() *DefaultArgs {
	return &DefaultArgs{
		Input: []string{
			"-re",
			"-i", "<input_url>",
			"-c", "copy",
			"-f", "rtsp",
			"-rtsp_transport", "tcp",
			"-progress", "pipe:1",
			"<output_rtsp_url>",
		},
		Output: []string{
			"-hide_banner",
			"-loglevel", "info",
			"-stats",
			"-re",
			"-i", "<local_rtsp_url>",
			// Video codec options (when specified)
			"-c:v", "<video_codec>",
			"-r", "<framerate>",
			"-s", "<resolution>",
			"-b:v", "<bitrate>",
			// Audio codec options (when specified)
			"-c:a", "<audio_codec>",
			// Rotation/filters (when specified)
			"-vf", "<rotation_filter>",
			// Output format and URL
			"-f", "flv",
			"<output_url>",
		},
		Recording: []string{
			"-hide_banner",
			"-loglevel", "info",
			"-i", "<local_rtsp_url>",
			"-c", "copy",
			"-y",
			"<output_file>",
		},
		HLS: []string{
			"-rtsp_transport", "tcp",
			"-analyzeduration", "500k",
			"-probesize", "500k",
			"-fflags", "nobuffer",
			"-i", "<local_rtsp_url>",
			"-c:v", "libx264",
			"-preset", "ultrafast",
			"-tune", "zerolatency",
			"-c:a", "aac",
			"-ac", "2",
			"-ar", "44100",
			"-f", "hls",
			"-hls_time", "2",
			"-hls_list_size", "6",
			"-hls_flags", "delete_segments+append_list",
			"-hls_segment_filename", "<segment_pattern>",
			"-y",
			"<playlist_file>",
		},
	}
}
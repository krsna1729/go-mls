package worker

import (
	"context"
	"time"
)

type Process interface {
	PID() int
	Stop()
	Wait() error
	Done() <-chan struct{}
	Err() error
}

type ProcessCreator interface {
	NewInputProcess(ctx context.Context, sourceURL, destURL string) (Process, error)
	NewOutputProcess(ctx context.Context, sourceURL, destURL string) (Process, error)
	NewRecordProcess(ctx context.Context, sourceURL, destPath string) (Process, error)
	NewHLSProcess(ctx context.Context, sourceURL, destDir, preset string) (Process, error)
}

type DefaultProcessCreator struct{}

func (f *DefaultProcessCreator) NewInputProcess(ctx context.Context, sourceURL, destURL string) (Process, error) {
	args := []string{
		"-re",
		"-i", sourceURL,
		"-c", "copy",
		"-f", "rtsp",
		"-rtsp_transport", "tcp",
		"-progress", "pipe:1",
		destURL,
	}
	return RunAndMonitorFFmpeg(ctx, nil, nil, args...)
}

func (f *DefaultProcessCreator) NewOutputProcess(ctx context.Context, sourceURL, destURL string) (Process, error) {
	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-stats",
		"-re",
		"-i", sourceURL,
		"-f", "flv",
		"-progress", "pipe:1",
		destURL,
	}
	return RunAndMonitorFFmpeg(ctx, nil, nil, args...)
}

func (f *DefaultProcessCreator) NewRecordProcess(ctx context.Context, sourceURL, destPath string) (Process, error) {
	args := []string{
		"-i", sourceURL,
		"-c", "copy",
		"-movflags", "+faststart",
		destPath,
	}
	return RunAndMonitorFFmpeg(ctx, nil, nil, args...)
}

func (f *DefaultProcessCreator) NewHLSProcess(ctx context.Context, sourceURL, destDir, preset string) (Process, error) {
	args := []string{
		"-re",
		"-rtsp_transport", "tcp",
		"-analyzeduration", "500k",
		"-probesize", "500k",
		"-fflags", "nobuffer",
		"-i", sourceURL,
		"-c:v", "libx264",
		"-preset", preset,
		"-tune", "zerolatency",
		"-c:a", "aac",
		"-ac", "2",
		"-ar", "44100",
		"-f", "hls",
		"-hls_time", "2",
		"-hls_list_size", "5",
		"-hls_flags", "delete_segments+append_list",
		"-hls_segment_filename", destDir + "/segment_%03d.ts",
		destDir + "/index.m3u8",
	}
	return RunAndMonitorFFmpeg(ctx, nil, nil, args...)
}

type FFmpegProcessConfig struct {
	StopTimeout time.Duration
}

type NoopFFmpegProcess struct{}

func (p *NoopFFmpegProcess) PID() int    { return 0 }
func (p *NoopFFmpegProcess) Stop()       {}
func (p *NoopFFmpegProcess) Wait() error { return nil }
func (p *NoopFFmpegProcess) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
func (p *NoopFFmpegProcess) Err() error { return nil }

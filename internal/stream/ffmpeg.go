package stream

import (
	"context"
	"strconv"
	"strings"
	"time"
)

type FFmpegOpts struct {
	VideoCodec string
	AudioCodec string
	Resolution string
	Framerate  string
	Bitrate    string
	ExtraArgs  []string
}

type FFmpegFactory interface {
	NewInputProcess(ctx context.Context, src, dst string) (FFmpegProcess, error)
	NewOutputProcess(ctx context.Context, src, dst string, opts FFmpegOpts) (FFmpegProcess, error)
	NewHLSProcess(ctx context.Context, src, dir string, preset string) (FFmpegProcess, error)
	NewRecordProcess(ctx context.Context, src, file string) (FFmpegProcess, error)
}

type defaultFFmpegFactory struct{}

func NewDefaultFFmpegFactory() FFmpegFactory {
	return &defaultFFmpegFactory{}
}

func (f *defaultFFmpegFactory) NewInputProcess(ctx context.Context, src, dst string) (FFmpegProcess, error) {
	args := []string{
		"-re",
		"-i", src,
		"-c", "copy",
		"-f", "rtsp",
		"-rtsp_transport", "tcp",
		"-progress", "pipe:1",
		dst,
	}
	return NewFFmpegProcess(ctx, args...)
}

func (f *defaultFFmpegFactory) NewOutputProcess(ctx context.Context, src, dst string, opts FFmpegOpts) (FFmpegProcess, error) {
	args := []string{
		"-hide_banner",
		"-loglevel", "info",
		"-stats",
		"-re",
		"-i", src,
	}

	if opts.VideoCodec != "" {
		args = append(args, "-c:v", opts.VideoCodec)
	}
	if opts.AudioCodec != "" {
		args = append(args, "-c:a", opts.AudioCodec)
	}
	if opts.Resolution != "" {
		args = append(args, "-s", opts.Resolution)
	}
	if opts.Framerate != "" {
		args = append(args, "-r", opts.Framerate)
	}
	if opts.Bitrate != "" {
		args = append(args, "-b:v", opts.Bitrate)
	}
	if len(opts.ExtraArgs) > 0 {
		args = append(args, opts.ExtraArgs...)
	}

	args = append(args, "-f", "flv", dst)
	args = append(args, "-progress", "pipe:1")

	return NewFFmpegProcess(ctx, args...)
}

func (f *defaultFFmpegFactory) NewHLSProcess(ctx context.Context, src, dir string, preset string) (FFmpegProcess, error) {
	if preset == "" {
		preset = "ultrafast"
	}
	playlist := dir + "/index.m3u8"
	segmentPattern := dir + "/segment_%03d.ts"
	args := []string{
		"-re",
		"-rtsp_transport", "tcp",
		"-analyzeduration", "500k",
		"-probesize", "500k",
		"-fflags", "nobuffer",
		"-i", src,
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
		"-hls_segment_filename", segmentPattern,
		"-y",
		playlist,
	}
	return NewFFmpegProcess(ctx, args...)
}

func (f *defaultFFmpegFactory) NewRecordProcess(ctx context.Context, src, file string) (FFmpegProcess, error) {
	args := []string{
		"-y",
		"-i", src,
		"-c", "copy",
		file,
	}
	return NewFFmpegProcess(ctx, args...)
}

type testFFmpegFactory struct {
	procs map[string]FFmpegProcess
}

func NewTestFFmpegFactory() *testFFmpegFactory {
	return &testFFmpegFactory{
		procs: make(map[string]FFmpegProcess),
	}
}

func (f *testFFmpegFactory) NewInputProcess(ctx context.Context, src, dst string) (FFmpegProcess, error) {
	return &mockFFmpegProcess{}, nil
}

func (f *testFFmpegFactory) NewOutputProcess(ctx context.Context, src, dst string, opts FFmpegOpts) (FFmpegProcess, error) {
	return &mockFFmpegProcess{}, nil
}

func (f *testFFmpegFactory) NewHLSProcess(ctx context.Context, src, dir string, preset string) (FFmpegProcess, error) {
	return &mockFFmpegProcess{}, nil
}

func (f *testFFmpegFactory) NewRecordProcess(ctx context.Context, src, file string) (FFmpegProcess, error) {
	return &mockFFmpegProcess{}, nil
}

type mockFFmpegProcess struct {
	speed   float64
	bitrate float64
	running bool
}

func (m *mockFFmpegProcess) Start(ctx context.Context) error {
	m.running = true
	return nil
}

func (m *mockFFmpegProcess) Stop(ctx context.Context, timeout time.Duration) error {
	m.running = false
	return nil
}

func (m *mockFFmpegProcess) Wait() error {
	return nil
}

func (m *mockFFmpegProcess) GetOutput() string {
	return ""
}

func (m *mockFFmpegProcess) GetSpeed() (float64, time.Time) {
	return m.speed, time.Now()
}

func (m *mockFFmpegProcess) GetBitrate() (float64, bool) {
	return m.bitrate, m.bitrate > 0
}

func (m *mockFFmpegProcess) GetPID() int {
	return 12345
}

func (m *mockFFmpegProcess) OutputChannel() <-chan string {
	ch := make(chan string)
	close(ch)
	return ch
}

func FFmpegOptsFromMap(m map[string]string) FFmpegOpts {
	opts := FFmpegOpts{}
	if v, ok := m["video_codec"]; ok {
		opts.VideoCodec = v
	}
	if v, ok := m["audio_codec"]; ok {
		opts.AudioCodec = v
	}
	if v, ok := m["resolution"]; ok {
		opts.Resolution = v
	}
	if v, ok := m["framerate"]; ok {
		opts.Framerate = v
	}
	if v, ok := m["bitrate"]; ok {
		opts.Bitrate = v
	}
	return opts
}

func FFmpegOptsToMap(opts FFmpegOpts) map[string]string {
	m := make(map[string]string)
	if opts.VideoCodec != "" {
		m["video_codec"] = opts.VideoCodec
	}
	if opts.AudioCodec != "" {
		m["audio_codec"] = opts.AudioCodec
	}
	if opts.Resolution != "" {
		m["resolution"] = opts.Resolution
	}
	if opts.Framerate != "" {
		m["framerate"] = opts.Framerate
	}
	if opts.Bitrate != "" {
		m["bitrate"] = opts.Bitrate
	}
	return m
}

func ParsePreset(presetName string) FFmpegOpts {
	switch presetName {
	case "YouTube":
		return FFmpegOpts{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "1920x1080",
			Framerate:  "30",
			Bitrate:    "4500k",
		}
	case "Facebook":
		return FFmpegOpts{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "1280x720",
			Framerate:  "30",
			Bitrate:    "2500k",
		}
	case "Twitch":
		return FFmpegOpts{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "1920x1080",
			Framerate:  "60",
			Bitrate:    "6000k",
		}
	case "Instagram":
		return FFmpegOpts{
			VideoCodec: "libx264",
			AudioCodec: "aac",
			Resolution: "720x1280",
			Framerate:  "30",
			Bitrate:    "3500k",
		}
	default:
		return FFmpegOpts{}
	}
}

func ParseSpeed(s string) (float64, error) {
	s = strings.TrimSuffix(s, "x")
	s = strings.TrimSpace(s)
	return strconv.ParseFloat(s, 64)
}

func ParseBitrate(s string) (float64, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "kbits/s")
	s = strings.TrimSuffix(s, "kbits")
	s = strings.TrimSpace(s)
	return strconv.ParseFloat(s, 64)
}

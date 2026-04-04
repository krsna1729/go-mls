package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go-mls/internal/logger"
	"go-mls/internal/state"
)

type Recorder struct {
	*ProcessWorker
	store      *state.Store
	log        *logger.Logger
	recording  *state.Recording
	recDir     string
	rtmpPort   int
	streamPath string
	outPath    string
}

func StartRecorder(ctx context.Context, store *state.Store, log *logger.Logger, streamPath, recDir string, rtmpPort int) (*Recorder, error) {
	if _, exists := store.GetRecording(streamPath); exists {
		return nil, fmt.Errorf("recording for %q already active", streamPath)
	}

	if err := os.MkdirAll(recDir, 0755); err != nil {
		return nil, fmt.Errorf("create recording dir: %w", err)
	}

	ts := time.Now().Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("%s_%s.mp4", streamPath, ts)
	outPath := filepath.Join(recDir, filename)
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return nil, fmt.Errorf("create recording parent dir: %w", err)
	}

	localInput := fmt.Sprintf("rtmp://127.0.0.1:%d/%s", rtmpPort, streamPath)

	args := []string{
		"-i", localInput,
		"-c", "copy",
		"-movflags", "+faststart",
		outPath,
	}

	r := &Recorder{
		store:      store,
		log:        log.With("component", "recorder", "stream_path", streamPath),
		recDir:     recDir,
		rtmpPort:   rtmpPort,
		streamPath: streamPath,
		outPath:    outPath,
	}

	factory := func(ctx context.Context) (Process, error) {
		fp, err := RunAndMonitorFFmpeg(ctx, store, r.log, args...)
		if err != nil {
			return nil, fmt.Errorf("start recorder: %w", err)
		}

		rec := &state.Recording{
			StreamPath: streamPath,
			Filename:   filename,
			StartedAt:  time.Now(),
			Status:     state.RecordingStatusActive,
			PID:        fp.PID(),
		}

		if err := store.AddRecording(rec); err != nil {
			fp.Stop()
			return nil, err
		}

		r.recording = rec
		r.ProcessWorker = NewProcessWorker("recorder:"+streamPath, log).WithProcess(fp)
		return fp, nil
	}

	_, err := RunProcessWorker("recorder:"+streamPath, log, factory)
	if err != nil {
		return nil, err
	}

	return r, nil
}

func (r *Recorder) Stop() {
	if r.ProcessWorker != nil && r.ProcessWorker.proc != nil {
		r.ProcessWorker.proc.Stop()
	}
	if r.recording != nil {
		r.recording.Status = state.RecordingStatusStopped
		r.log.Info("Recording stopped", "filename", r.recording.Filename)
	}
}

func (r *Recorder) Done() <-chan struct{} {
	if r.ProcessWorker != nil {
		return r.ProcessWorker.Done()
	}
	ch := make(chan struct{})
	close(ch)
	return ch
}

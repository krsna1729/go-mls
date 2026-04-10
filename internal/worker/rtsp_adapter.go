package worker

import (
	"context"
	"fmt"

	"go-mls/internal/ffmpeg"
	"go-mls/internal/logger"
	"go-mls/internal/state"
)

// RTSPAdapter pulls a stream from the local RTSP ingest endpoint and bridges it
// into the internal RTMP backbone for downstream workers.
type RTSPAdapter struct {
	*ProcessWorker
	store      *state.Store
	stream     *state.Input
	rtspPort   int
	rtmpPort   int
	localInput string
}

func StartRTSPAdapter(ctx context.Context, store *state.Store, log *logger.Logger, stream *state.Input, rtspPort, rtmpPort int) (*RTSPAdapter, error) {
	localInput := fmt.Sprintf("rtsp://127.0.0.1:%d/%s", rtspPort, stream.StreamPath)
	localOutput := fmt.Sprintf("rtmp://127.0.0.1:%d/%s", rtmpPort, stream.StreamPath)

	args := []string{
		"-rtsp_transport", "tcp",
		"-i", localInput,
		"-c", "copy",
		"-f", "flv",
		localOutput,
	}

	a := &RTSPAdapter{
		store:         store,
		stream:        stream,
		rtspPort:      rtspPort,
		rtmpPort:      rtmpPort,
		localInput:    localInput,
		ProcessWorker: NewProcessWorker("rtsp-adapter:"+stream.StreamPath, log),
	}

	factory := func(ctx context.Context) (Process, error) {
		fp, err := ffmpeg.RunAndMonitor(ctx, store, log.With("stream_path", stream.StreamPath, "rtsp_input", localInput), args...)
		if err != nil {
			store.UpdateInputStatus(stream.StreamPath, state.InputStatusError, err.Error())
			return nil, fmt.Errorf("start rtsp adapter: %w", err)
		}
		store.UpdateInputPID(stream.StreamPath, fp.PID())
		a.ProcessWorker.WithProcess(fp)
		return fp, nil
	}

	_, err := a.ProcessWorker.StartWithFactory(ctx, factory)
	if err != nil {
		return nil, err
	}

	go func() {
		<-a.Done()
		if err := a.Wait(); err != nil {
			store.UpdateInputStatus(stream.StreamPath, state.InputStatusError, err.Error())
		}
	}()

	return a, nil
}

func (a *RTSPAdapter) Stop() {
	if a.ProcessWorker != nil {
		a.ProcessWorker.Stop()
		a.store.UpdateInputStatus(a.stream.StreamPath, state.InputStatusStopped, "")
	}
	a.log.Info("RTSP adapter stopped", "stream_path", a.stream.StreamPath)
}

func (a *RTSPAdapter) Done() <-chan struct{} {
	if a.ProcessWorker != nil {
		return a.ProcessWorker.Done()
	}
	ch := make(chan struct{})
	close(ch)
	return ch
}

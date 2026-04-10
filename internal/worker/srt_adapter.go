package worker

import (
	"context"
	"fmt"

	"go-mls/internal/ffmpeg"
	"go-mls/internal/logger"
	"go-mls/internal/state"
)

// SRTAdapter subscribes to a stream on the local shared SRT ingest hub and
// bridges it into the internal RTMP backbone for downstream workers.
type SRTAdapter struct {
	*ProcessWorker
	store      *state.Store
	stream     *state.Input
	srtPort    int
	rtmpPort   int
	localInput string
}

func StartSRTAdapter(ctx context.Context, store *state.Store, log *logger.Logger, stream *state.Input, srtPort, rtmpPort int) (*SRTAdapter, error) {
	streamID := "subscribe:" + stream.StreamPath
	localInput := fmt.Sprintf("srt://127.0.0.1:%d?mode=caller&streamid=%s&transtype=live&latency=120", srtPort, streamID)
	localOutput := fmt.Sprintf("rtmp://127.0.0.1:%d/%s", rtmpPort, stream.StreamPath)

	args := []string{
		"-i", localInput,
		"-c", "copy",
		"-f", "flv",
		localOutput,
	}

	a := &SRTAdapter{
		store:         store,
		stream:        stream,
		srtPort:       srtPort,
		rtmpPort:      rtmpPort,
		localInput:    localInput,
		ProcessWorker: NewProcessWorker("srt-adapter:"+stream.StreamPath, log),
	}

	factory := func(ctx context.Context) (Process, error) {
		fp, err := ffmpeg.RunAndMonitor(ctx, store, log.With("stream_path", stream.StreamPath, "srt_input", localInput), args...)
		if err != nil {
			store.UpdateInputStatus(stream.StreamPath, state.InputStatusError, err.Error())
			return nil, fmt.Errorf("start srt adapter: %w", err)
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

func (a *SRTAdapter) Stop() {
	if a.ProcessWorker != nil {
		a.ProcessWorker.Stop()
		a.store.UpdateInputStatus(a.stream.StreamPath, state.InputStatusStopped, "")
	}
	a.log.Info("SRT adapter stopped", "stream_path", a.stream.StreamPath)
}

func (a *SRTAdapter) Done() <-chan struct{} {
	if a.ProcessWorker != nil {
		return a.ProcessWorker.Done()
	}
	ch := make(chan struct{})
	close(ch)
	return ch
}

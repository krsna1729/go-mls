package worker

import (
	"context"
	"fmt"

	"go-mls/internal/logger"
	"go-mls/internal/state"
)

type Puller struct {
	*ProcessWorker
	store    *state.Store
	stream   *state.Input
	rtmpPort int
}

func StartPuller(ctx context.Context, store *state.Store, log *logger.Logger, stream *state.Input, rtmpPort int) (*Puller, error) {
	localURL := fmt.Sprintf("rtmp://127.0.0.1:%d/%s", rtmpPort, stream.StreamPath)

	args := []string{
		"-re",
		"-i", stream.RemoteURL,
		"-c", "copy",
		"-f", "flv",
		localURL,
	}

	p := &Puller{
		store:         store,
		stream:        stream,
		rtmpPort:      rtmpPort,
		ProcessWorker: NewProcessWorker("puller:"+stream.StreamPath, log),
	}

	factory := func(ctx context.Context) (Process, error) {
		fp, err := RunAndMonitorFFmpeg(ctx, store, log.With("stream_path", stream.StreamPath), args...)
		if err != nil {
			store.UpdateInputStatus(stream.StreamPath, state.InputStatusError, err.Error())
			return nil, fmt.Errorf("start puller: %w", err)
		}
		stream.PID = fp.PID()
		store.UpdateInputStatus(stream.StreamPath, state.InputStatusActive, "")
		p.ProcessWorker = p.ProcessWorker.WithProcess(fp)
		return fp, nil
	}

	_, err := p.ProcessWorker.StartWithFactory(factory)
	if err != nil {
		return nil, err
	}

	return p, nil
}

func (p *Puller) Stop() {
	if p.ProcessWorker != nil && p.ProcessWorker.proc != nil {
		p.ProcessWorker.proc.Stop()
		p.store.UpdateInputStatus(p.stream.StreamPath, state.InputStatusStopped, "")
	}
	p.log.Info("Puller stopped", "stream_path", p.stream.StreamPath)
}

func (p *Puller) PID() int {
	if p.ProcessWorker != nil && p.ProcessWorker.proc != nil {
		return p.ProcessWorker.proc.PID()
	}
	return 0
}

func (p *Puller) Done() <-chan struct{} {
	if p.ProcessWorker != nil {
		return p.ProcessWorker.Done()
	}
	ch := make(chan struct{})
	close(ch)
	return ch
}

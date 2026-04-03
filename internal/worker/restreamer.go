package worker

import (
	"context"
	"fmt"

	"go-mls/internal/logger"
	"go-mls/internal/state"
)

type Restreamer struct {
	*ProcessWorker
	store  *state.Store
	output *state.Output
}

func StartRestreamer(ctx context.Context, store *state.Store, log *logger.Logger, out *state.Output, rtmpPort int) (*Restreamer, error) {
	localInput := fmt.Sprintf("rtmp://127.0.0.1:%d/%s", rtmpPort, out.StreamPath)

	remoteURL := out.RemoteURL
	if out.StreamKey != "" {
		remoteURL = remoteURL + "/" + out.StreamKey
	}

	args := []string{"-i", localInput}

	if len(out.VideoArgs) > 0 {
		args = append(args, out.VideoArgs...)
	} else {
		args = append(args, "-c:v", "copy")
	}

	if len(out.AudioArgs) > 0 {
		args = append(args, out.AudioArgs...)
	} else {
		args = append(args, "-c:a", "copy")
	}

	args = append(args, "-f", "flv", remoteURL)

	r := &Restreamer{
		store:  store,
		output: out,
	}

	factory := func(ctx context.Context) (Process, error) {
		fp, err := RunAndMonitorFFmpeg(ctx, store, log.With("output_id", out.OutputID), args...)
		if err != nil {
			store.UpdateOutputStatus(out.StreamPath, out.OutputID, state.OutputStatusError, err.Error())
			return nil, fmt.Errorf("start restreamer: %w", err)
		}
		out.PID = fp.PID()
		store.UpdateOutputStatus(out.StreamPath, out.OutputID, state.OutputStatusRunning, "")
		r.ProcessWorker = NewProcessWorker("restreamer:"+out.OutputID, log).WithProcess(fp)
		return fp, nil
	}

	_, err := RunProcessWorker("restreamer:"+out.OutputID, log, factory)
	if err != nil {
		return nil, err
	}

	return r, nil
}

func (r *Restreamer) Stop() {
	if r.ProcessWorker != nil && r.ProcessWorker.proc != nil {
		r.ProcessWorker.proc.Stop()
		r.store.UpdateOutputStatus(r.output.StreamPath, r.output.OutputID, state.OutputStatusStopped, "")
	}
	if r.log != nil {
		r.log.Info("Restreamer stopped", "output_id", r.output.OutputID)
	}
}

func (r *Restreamer) Done() <-chan struct{} {
	if r.ProcessWorker != nil {
		return r.ProcessWorker.Done()
	}
	ch := make(chan struct{})
	close(ch)
	return ch
}

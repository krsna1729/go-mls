package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go-mls/internal/logger"
	"go-mls/internal/process"
)

type Pipeline struct {
	Logger  *logger.Logger
	RTSPSrv *RTSPServerManager
	FFmpeg  FFmpegFactory
	SSE     *SSEBroker
	recDir  string

	mu          sync.RWMutex
	relays      map[string]*Relay
	goroutineWG sync.WaitGroup

	ffmpegTimeout time.Duration
}

func NewPipeline(l *logger.Logger, recDir string, ffmpegTimeout time.Duration) *Pipeline {
	sse, err := NewSSEBrokerWithWatcher(recDir)
	if err != nil {
		l.Warn("Failed to create SSE watcher", "err", err)
		sse = NewSSEBroker()
	}

	return &Pipeline{
		Logger:        l,
		FFmpeg:        NewDefaultFFmpegFactory(),
		SSE:           sse,
		recDir:        recDir,
		relays:        make(map[string]*Relay),
		ffmpegTimeout: ffmpegTimeout,
	}
}

func (p *Pipeline) SetRTSPServer(srv *RTSPServerManager) {
	p.RTSPSrv = srv
}

func (p *Pipeline) SetFFmpegFactory(factory FFmpegFactory) {
	p.FFmpeg = factory
}

func (p *Pipeline) GetRecDir() string {
	return p.recDir
}

func (p *Pipeline) GetInputURL(name string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	relay, exists := p.relays[name]
	if !exists || relay.Input == nil {
		return "", false
	}
	return relay.Input.SourceURL, true
}

func (p *Pipeline) resolveInputURL(inputURL string) (string, error) {
	if strings.HasPrefix(inputURL, "file://") {
		relative := strings.TrimPrefix(inputURL, "file://")
		filePath := filepath.Join(p.recDir, relative)
		if _, err := os.Stat(filePath); err != nil {
			return "", err
		}
		p.Logger.Debug("Resolved input URL", "inputURL", inputURL, "filePath", filePath)
		return filePath, nil
	}
	return inputURL, nil
}

func (p *Pipeline) resolveOutputURL(outputURL string) (string, error) {
	if strings.HasPrefix(outputURL, "file://") {
		relative := strings.TrimPrefix(outputURL, "file://")
		filePath := filepath.Join(p.recDir, relative)
		p.Logger.Debug("Resolved output URL", "outputURL", outputURL, "filePath", filePath)
		return filePath, nil
	}
	return outputURL, nil
}

func (p *Pipeline) StartInput(ctx context.Context, name, sourceURL string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for relName, relay := range p.relays {
		if relay.Input != nil && relay.Input.SourceURL == sourceURL && relName != name {
			return fmt.Errorf("input URL %s already in use with name %s", sourceURL, relName)
		}
	}

	relay, exists := p.relays[name]
	if exists && relay.Input != nil {
		if relay.Input.Status == PStreamRunning || relay.Input.Status == PStreamStarting {
			return fmt.Errorf("input %s already running", name)
		}
	}

	if !exists {
		relay = &Relay{
			Outputs: make(map[string]*PipelineStream),
		}
		p.relays[name] = relay
	}

	inp := &PipelineStream{
		Name:      name,
		Type:      PTypeInput,
		SourceURL: sourceURL,
		Status:    PStreamStarting,
		CreatedAt: time.Now(),
	}
	relay.Input = inp

	if p.RTSPSrv == nil {
		inp.Status = PStreamError
		inp.LastError = "RTSP server not configured"
		return fmt.Errorf("RTSP server not configured")
	}

	relayPath := fmt.Sprintf("relay/%s", name)
	localURL := p.RTSPSrv.GetRTSPURL(relayPath)
	inp.LocalURL = localURL

	resolvedURL, err := p.resolveInputURL(sourceURL)
	if err != nil {
		inp.Status = PStreamError
		inp.LastError = err.Error()
		return fmt.Errorf("failed to resolve input URL: %w", err)
	}

	proc, err := p.FFmpeg.NewInputProcess(ctx, resolvedURL, localURL)
	if err != nil {
		inp.Status = PStreamError
		inp.LastError = err.Error()
		return fmt.Errorf("failed to create ffmpeg process: %w", err)
	}
	inp.Proc = proc

	if err := proc.Start(ctx); err != nil {
		inp.Status = PStreamError
		inp.LastError = err.Error()
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	inp.Status = PStreamRunning
	inp.StartedAt = time.Now()

	p.goroutineWG.Add(1)
	go func() {
		defer p.goroutineWG.Done()
		p.runInputRelay(name, inp)
	}()

	p.Logger.Info("Waiting for RTSP stream to become ready", "relayPath", relayPath)
	if err := p.RTSPSrv.WaitForStreamReady(relayPath, p.ffmpegTimeout); err != nil {
		p.Logger.Warn("Failed to wait for RTSP stream", "relayPath", relayPath, "err", err)
	}

	p.Logger.Info("Started input relay", "name", name, "sourceURL", sourceURL, "localURL", localURL)
	return nil
}

func (p *Pipeline) runInputRelay(name string, inp *PipelineStream) {
	p.mu.Lock()
	if inp.Proc == nil {
		p.mu.Unlock()
		return
	}
	proc := inp.Proc
	currentStatus := inp.Status
	p.mu.Unlock()

	err := proc.Wait()

	p.mu.Lock()
	if inp.Proc != proc {
		p.mu.Unlock()
		return
	}
	output := inp.Proc.GetOutput()
	if err != nil {
		inp.Status = PStreamError
		inp.LastError = err.Error()
	} else {
		inp.Status = PStreamStopped
	}
	p.mu.Unlock()

	if currentStatus == PStreamRunning {
		if err != nil {
			p.Logger.Error("Input relay exited with error", "name", name, "err", err, "output", output)
		} else {
			p.Logger.Info("Input relay stopped", "name", name)
		}
	}

	p.cleanupInput(name)
}

func (p *Pipeline) cleanupInput(name string) {
	p.mu.Lock()
	relay, ok := p.relays[name]
	if !ok {
		p.mu.Unlock()
		return
	}

	if relay.Input != nil && relay.Input.Proc != nil {
		relay.Input.Proc.Stop(context.Background(), 2*time.Second)
		relay.Input.Proc = nil
	}

	if relay.Input != nil && (relay.Input.Status == PStreamStopped || relay.Input.Status == PStreamError) {
		relay.Input = nil
	}

	shouldDelete := relay.Input == nil && len(relay.Outputs) == 0 && relay.Recording == nil && relay.HLSSession == nil
	p.mu.Unlock()

	if shouldDelete {
		if p.RTSPSrv != nil {
			relayPath := "relay/" + name
			p.RTSPSrv.RemoveStream(relayPath)
		}
		p.mu.Lock()
		delete(p.relays, name)
		p.mu.Unlock()
	}
}

func (p *Pipeline) StopInput(name string) error {
	p.mu.Lock()
	relay, ok := p.relays[name]
	if !ok || relay.Input == nil {
		p.mu.Unlock()
		return fmt.Errorf("input %s not found", name)
	}

	if relay.Input.RefCount > 0 {
		p.mu.Unlock()
		return fmt.Errorf("input %s still has %d consumers", name, relay.Input.RefCount)
	}

	relay.Input.Status = PStreamStopped
	if relay.Input.Proc != nil {
		relay.Input.Proc.Stop(context.Background(), 2*time.Second)
		relay.Input.Proc = nil
	}
	p.mu.Unlock()

	p.cleanupInput(name)
	p.Logger.Info("Stopped input relay", "name", name)
	return nil
}

func (p *Pipeline) StartOutput(ctx context.Context, name, inputName, destURL string, opts FFmpegOpts, preset string) error {
	p.mu.Lock()
	relay, exists := p.relays[inputName]
	if !exists || relay.Input == nil {
		p.mu.Unlock()
		return fmt.Errorf("input %s not found", inputName)
	}

	if _, exists := relay.Outputs[name]; exists {
		if relay.Outputs[name].Status == PStreamRunning {
			p.mu.Unlock()
			return fmt.Errorf("output %s already running", name)
		}
	}

	out := &PipelineStream{
		Name:      name,
		Type:      PTypeOutput,
		SourceURL: relay.Input.SourceURL,
		LocalURL:  relay.Input.LocalURL,
		Status:    PStreamStarting,
		CreatedAt: time.Now(),
		Preset:    preset,
	}
	relay.Outputs[name] = out
	relay.Input.IncrementRef()
	p.mu.Unlock()

	if relay.Input.LocalURL == "" {
		out.Status = PStreamError
		out.LastError = "input not ready (no local URL)"
		p.mu.Lock()
		delete(relay.Outputs, name)
		p.mu.Unlock()
		return fmt.Errorf("input not ready")
	}

	resolvedDestURL, err := p.resolveOutputURL(destURL)
	if err != nil {
		out.Status = PStreamError
		out.LastError = err.Error()
		p.mu.Lock()
		delete(relay.Outputs, name)
		p.mu.Unlock()
		return fmt.Errorf("failed to resolve output URL: %w", err)
	}

	proc, err := p.FFmpeg.NewOutputProcess(ctx, relay.Input.LocalURL, resolvedDestURL, opts)
	if err != nil {
		out.Status = PStreamError
		out.LastError = err.Error()
		p.mu.Lock()
		delete(relay.Outputs, name)
		p.mu.Unlock()
		return fmt.Errorf("failed to create ffmpeg process: %w", err)
	}
	out.Proc = proc

	if err := proc.Start(ctx); err != nil {
		out.Status = PStreamError
		out.LastError = err.Error()
		p.mu.Lock()
		delete(relay.Outputs, name)
		p.mu.Unlock()
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	out.Status = PStreamRunning
	out.StartedAt = time.Now()

	p.goroutineWG.Add(1)
	go func() {
		defer p.goroutineWG.Done()
		p.runOutputRelay(name, inputName, out)
	}()

	p.Logger.Info("Started output relay", "name", name, "inputName", inputName, "destURL", destURL)
	return nil
}

func (p *Pipeline) runOutputRelay(name, inputName string, out *PipelineStream) {
	p.mu.Lock()
	if out.Proc == nil {
		p.mu.Unlock()
		return
	}
	proc := out.Proc
	p.mu.Unlock()

	err := proc.Wait()

	p.mu.Lock()
	out.Status = PStreamStopped
	if err != nil {
		out.Status = PStreamError
		out.LastError = err.Error()
	}
	p.mu.Unlock()

	p.StopOutput(name)
}

func (p *Pipeline) StopOutput(name string) error {
	p.mu.Lock()

	var relay *Relay
	var out *PipelineStream

	for _, rel := range p.relays {
		if o, exists := rel.Outputs[name]; exists {
			relay = rel
			out = o
			break
		}
	}

	if out == nil {
		p.mu.Unlock()
		return nil
	}

	out.Status = PStreamStopped
	if out.Proc != nil {
		out.Proc.Stop(context.Background(), 2*time.Second)
		out.Proc = nil
	}

	if relay != nil {
		delete(relay.Outputs, name)
		if relay.Input != nil {
			relay.Input.DecrementRef()
		}
	}

	shouldDeleteRelay := relay != nil && relay.Input == nil && len(relay.Outputs) == 0 && relay.Recording == nil && relay.HLSSession == nil
	p.mu.Unlock()

	if shouldDeleteRelay && relay != nil {
		if p.RTSPSrv != nil {
			relayPath := "relay/" + relay.Input.Name
			p.RTSPSrv.RemoveStream(relayPath)
		}
		p.mu.Lock()
		delete(p.relays, relay.Input.Name)
		p.mu.Unlock()
	}

	p.SSE.Broadcast("update")
	p.Logger.Info("Stopped output relay", "name", name)
	return nil
}

func (p *Pipeline) StartRecording(ctx context.Context, name, inputName string) error {
	p.mu.Lock()
	relay, exists := p.relays[inputName]
	if !exists || relay.Input == nil {
		p.mu.Unlock()
		return fmt.Errorf("input %s not found", inputName)
	}

	if relay.Recording != nil && relay.Recording.Active {
		p.mu.Unlock()
		return fmt.Errorf("active recording for %s already exists", inputName)
	}

	rec := &PipelineRecording{
		Name:      name,
		SourceURL: relay.Input.SourceURL,
		Filename:  fmt.Sprintf("%s_%d.mp4", name, time.Now().Unix()),
		FilePath:  filepath.Join(p.recDir, fmt.Sprintf("%s_%d.mp4", name, time.Now().Unix())),
		StartedAt: time.Now(),
		Active:    true,
	}
	relay.Recording = rec
	relay.Input.IncrementRef()
	p.mu.Unlock()

	if relay.Input.LocalURL == "" {
		p.mu.Lock()
		relay.Recording = nil
		p.mu.Unlock()
		return fmt.Errorf("input not ready")
	}

	proc, err := p.FFmpeg.NewRecordProcess(ctx, relay.Input.LocalURL, rec.FilePath)
	if err != nil {
		p.mu.Lock()
		relay.Recording = nil
		p.mu.Unlock()
		return fmt.Errorf("failed to create ffmpeg process: %w", err)
	}

	if err := proc.Start(ctx); err != nil {
		p.mu.Lock()
		relay.Recording = nil
		p.mu.Unlock()
		return fmt.Errorf("failed to start recording: %w", err)
	}

	p.mu.Lock()
	relay.Recording.Proc = proc
	p.mu.Unlock()

	p.goroutineWG.Add(1)
	go func() {
		defer p.goroutineWG.Done()
		p.runRecording(name, inputName, proc)
	}()

	p.SSE.Broadcast("update")
	p.Logger.Info("Started recording", "name", name, "inputName", inputName, "file", rec.FilePath)
	return nil
}

func (p *Pipeline) runRecording(name, inputName string, proc FFmpegProcess) {
	err := proc.Wait()

	p.mu.Lock()
	var relay *Relay
	for _, rel := range p.relays {
		if rel.Recording != nil && rel.Recording.Name == name && rel.Recording.Active {
			relay = rel
			break
		}
	}

	if relay != nil && relay.Recording != nil {
		relay.Recording.Active = false
		relay.Recording.StoppedAt = time.Now()
		if err != nil {
			p.Logger.Error("Recording failed", "name", name, "err", err)
		}
		if fi, statErr := os.Stat(relay.Recording.FilePath); statErr == nil {
			relay.Recording.FileSize = fi.Size()
		}
		relay.Recording = nil
	}

	if relay != nil && relay.Input != nil {
		relay.Input.DecrementRef()
	}
	p.mu.Unlock()

	p.SSE.Broadcast("update")
}

func (p *Pipeline) StopRecording(name string) error {
	p.mu.Lock()
	var relay *Relay
	for _, rel := range p.relays {
		if rel.Recording != nil && rel.Recording.Name == name && rel.Recording.Active {
			relay = rel
			break
		}
	}

	if relay == nil {
		p.mu.Unlock()
		return fmt.Errorf("no active recording found for %s", name)
	}

	relay.Recording.Active = false
	if relay.Recording.Proc != nil {
		relay.Recording.Proc.Stop(context.Background(), 2*time.Second)
		relay.Recording.Proc = nil
	}
	p.mu.Unlock()

	p.SSE.Broadcast("update")
	p.Logger.Info("Stopped recording", "name", name)
	return nil
}

func (p *Pipeline) ListRecordings() []*PipelineRecording {
	p.mu.RLock()
	var recs []*PipelineRecording
	for _, rel := range p.relays {
		if rel.Recording != nil {
			rec := *rel.Recording
			if rec.Active && rec.FilePath != "" {
				if fi, err := os.Stat(rec.FilePath); err == nil {
					rec.FileSize = fi.Size()
				}
			}
			recs = append(recs, &rec)
		}
	}
	p.mu.RUnlock()
	return recs
}

func (p *Pipeline) DeleteRecording(filename string) error {
	filePath := filepath.Join(p.recDir, filename)
	if err := os.Remove(filePath); err != nil {
		return err
	}

	p.SSE.Broadcast("update")
	return nil
}

func (p *Pipeline) StartHLS(ctx context.Context, name, inputName string, preset string) error {
	relay, exists := p.relays[inputName]
	if !exists || relay.Input == nil {
		return fmt.Errorf("input %s not found", inputName)
	}

	if relay.HLSSession != nil {
		return nil
	}

	hlsDir := filepath.Join(p.recDir, "hls", inputName)
	if err := os.MkdirAll(hlsDir, 0755); err != nil {
		return fmt.Errorf("failed to create HLS directory: %w", err)
	}

	sess := NewPipelineHLSSession(inputName, relay.Input.SourceURL)
	sess.LocalURL = relay.Input.LocalURL
	sess.Dir = hlsDir
	sess.Status = PStreamRunning

	relay.HLSSession = sess
	relay.Input.IncrementRef()

	if relay.Input.LocalURL == "" {
		relay.HLSSession = nil
		return fmt.Errorf("input not ready")
	}

	proc, err := p.FFmpeg.NewHLSProcess(ctx, relay.Input.LocalURL, hlsDir, preset)
	if err != nil {
		relay.HLSSession = nil
		return fmt.Errorf("failed to create HLS process: %w", err)
	}

	sess.Proc = proc

	if err := proc.Start(ctx); err != nil {
		sess.Status = PStreamError
		relay.HLSSession = nil
		return fmt.Errorf("failed to start HLS: %w", err)
	}

	p.goroutineWG.Add(1)
	go func() {
		defer p.goroutineWG.Done()
		p.runHLSSession(inputName, name, sess)
	}()

	p.Logger.Info("Started HLS session", "name", name, "inputName", inputName, "dir", hlsDir)
	return nil
}

func (p *Pipeline) runHLSSession(inputName, name string, sess *PipelineHLSSession) {
	err := sess.Proc.Wait()

	p.mu.Lock()
	relay, _ := p.relays[inputName]
	if sess != nil {
		if err != nil {
			sess.Status = PStreamError
			sess.LastError = err.Error()
		}
		sess.Status = PStreamStopped
		os.RemoveAll(sess.Dir)
		if relay != nil {
			relay.HLSSession = nil
		}
	}

	if relay != nil && relay.Input != nil {
		relay.Input.DecrementRef()
	}
	p.mu.Unlock()
}

func (p *Pipeline) StopHLS(name string) error {
	p.mu.Lock()
	var relay *Relay
	for _, rel := range p.relays {
		if rel.HLSSession != nil && rel.HLSSession.Name == name {
			relay = rel
			break
		}
	}
	if relay != nil {
		if relay.HLSSession.Proc != nil {
			relay.HLSSession.Proc.Stop(context.Background(), 2*time.Second)
		}
		os.RemoveAll(relay.HLSSession.Dir)
		relay.HLSSession = nil
	}
	p.mu.Unlock()

	if relay != nil {
		p.Logger.Info("Stopped HLS session", "name", name)
	}
	return nil
}

func (p *Pipeline) DeleteInput(inputName string) error {
	p.mu.Lock()
	relay, exists := p.relays[inputName]
	if !exists {
		p.mu.Unlock()
		return fmt.Errorf("input %s not found", inputName)
	}

	for name := range relay.Outputs {
		if out := relay.Outputs[name]; out != nil && out.Proc != nil {
			out.Proc.Stop(context.Background(), 2*time.Second)
		}
	}
	relay.Outputs = make(map[string]*PipelineStream)

	if relay.Recording != nil && relay.Recording.Active {
		relay.Recording.Active = false
	}

	if relay.HLSSession != nil {
		if relay.HLSSession.Proc != nil {
			relay.HLSSession.Proc.Stop(context.Background(), 2*time.Second)
		}
		os.RemoveAll(relay.HLSSession.Dir)
		relay.HLSSession = nil
	}

	if relay.Input != nil && relay.Input.Proc != nil {
		relay.Input.Proc.Stop(context.Background(), 2*time.Second)
		relay.Input.Proc = nil
	}

	delete(p.relays, inputName)
	p.mu.Unlock()

	if p.RTSPSrv != nil {
		relayPath := "relay/" + inputName
		p.RTSPSrv.RemoveStream(relayPath)
	}

	p.SSE.Broadcast("update")
	return nil
}

func (p *Pipeline) Status() StatusResponse {
	srv, _ := process.GetSelfUsage()

	p.mu.RLock()
	relays := make([]RelayStatus, 0, len(p.relays))

	for name, relay := range p.relays {
		if relay.Input == nil {
			continue
		}

		speed := 0.0
		if relay.Input.Proc != nil {
			speed, _ = relay.Input.Proc.GetSpeed()
		}

		input := RelayInputStatus{
			InputName:       name,
			InputURL:        relay.Input.SourceURL,
			LocalURL:        relay.Input.LocalURL,
			Status:          relay.Input.Status.String(),
			LastError:       relay.Input.LastError,
			RefCount:        relay.Input.RefCount,
			Speed:           speed,
			RecordingActive: relay.Recording != nil && relay.Recording.Active,
		}

		outputs := make([]RelayOutputStatus, 0, len(relay.Outputs))
		for _, out := range relay.Outputs {
			bitrate := 0.0
			if out.Proc != nil {
				if br, ok := out.Proc.GetBitrate(); ok {
					bitrate = br
				}
			}
			outputs = append(outputs, RelayOutputStatus{
				OutputName: out.Name,
				Status:     out.Status.String(),
				LastError:  out.LastError,
				Preset:     out.Preset,
				Bitrate:    bitrate,
			})
		}

		relays = append(relays, RelayStatus{
			Input:   input,
			Outputs: outputs,
		})
	}
	p.mu.RUnlock()

	return StatusResponse{
		Server: PipelineServerStatus{
			CPU: srv.CPU,
			Mem: srv.Mem,
		},
		Relays: relays,
	}
}

func (p *Pipeline) ExportConfig(filename string) error {
	p.mu.RLock()
	var configs []map[string]interface{}

	for name, relay := range p.relays {
		if relay.Input == nil || relay.Input.Status != PStreamRunning {
			continue
		}

		var outputs []map[string]string
		for _, out := range relay.Outputs {
			outputs = append(outputs, map[string]string{
				"output_name": out.Name,
			})
		}

		configs = append(configs, map[string]interface{}{
			"input_url":  relay.Input.SourceURL,
			"input_name": name,
			"outputs":    outputs,
		})
	}
	p.mu.RUnlock()

	data, err := json.MarshalIndent(configs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

func (p *Pipeline) ImportConfig(filename string) error {
	return nil
}

func (p *Pipeline) Shutdown() {
	p.Logger.Info("Pipeline: Shutting down...")

	p.mu.Lock()
	for _, relay := range p.relays {
		for _, out := range relay.Outputs {
			if out.Proc != nil {
				out.Proc.Stop(context.Background(), 2*time.Second)
			}
		}
		if relay.Input != nil && relay.Input.Proc != nil {
			relay.Input.Proc.Stop(context.Background(), 2*time.Second)
		}
		if relay.HLSSession != nil && relay.HLSSession.Proc != nil {
			relay.HLSSession.Proc.Stop(context.Background(), 2*time.Second)
		}
	}
	p.mu.Unlock()

	p.goroutineWG.Wait()
	p.SSE.Shutdown()
	p.Logger.Info("Pipeline: Shutdown complete")
}

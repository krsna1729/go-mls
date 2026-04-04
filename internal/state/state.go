// Package state provides the in-memory state model for go-mls.
// It tracks all active inputs, outputs, recordings, and HLS sessions.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// InputMode describes how an input source is ingested.
type InputMode string

const (
	InputModePull   InputMode = "pull"   // Active pull (Native RTMP or FFmpeg)
	InputModeAccept InputMode = "accept" // Passive acceptor (waiting for push)
)

// InputStatus describes the lifecycle state of an input.
type InputStatus string

const (
	InputStatusStarting InputStatus = "Starting"
	InputStatusActive   InputStatus = "Active"
	InputStatusStopped  InputStatus = "Stopped"
	InputStatusError    InputStatus = "Error"
)

// Input represents a registered input stream.
type Input struct {
	StreamPath  string    `json:"stream_path"`
	RemoteURL   string    `json:"remote_url,omitempty"`
	IngestToken string    `json:"ingest_token,omitempty"`
	Mode        InputMode `json:"mode"`
	RemoteAddr  string    `json:"-"`

	// Runtime state (not persisted)
	Status    InputStatus `json:"-"`
	LastError string      `json:"-"`
	PID       int         `json:"-"` // FFmpeg PID if applicable
}

// OutputStatus describes the lifecycle state of an output.
type OutputStatus string

const (
	OutputStatusStarting OutputStatus = "Starting"
	OutputStatusRunning  OutputStatus = "Running"
	OutputStatusStopped  OutputStatus = "Stopped"
	OutputStatusError    OutputStatus = "Error"
)

// Output represents an active output worker (restreamer).
type Output struct {
	StreamPath     string            `json:"stream_path"`
	OutputID       string            `json:"output_id"`
	RemoteURL      string            `json:"remote_url"`
	StreamKey      string            `json:"stream_key,omitempty"`
	VideoArgs      []string          `json:"video_args,omitempty"`
	AudioArgs      []string          `json:"audio_args,omitempty"`
	PlatformPreset string            `json:"platform_preset,omitempty"`
	FFmpegOptions  map[string]string `json:"ffmpeg_options,omitempty"`

	// Runtime state (not persisted)
	Status    OutputStatus `json:"-"`
	LastError string       `json:"-"`
	PID       int          `json:"-"`
}

// RecordingStatus describes the lifecycle state of a recording.
type RecordingStatus string

const (
	RecordingStatusActive  RecordingStatus = "Active"
	RecordingStatusStopped RecordingStatus = "Stopped"
)

// Recording represents an active MP4 recording.
type Recording struct {
	StreamPath string    `json:"stream_path"`
	Filename   string    `json:"filename"`
	StartedAt  time.Time `json:"started_at"`

	// Runtime state
	Status   RecordingStatus `json:"-"`
	FileSize int64           `json:"-"`
	PID      int             `json:"-"`
}

// HLSSession represents a lazy-loaded HLS generation session.
type HLSSession struct {
	StreamPath  string `json:"stream_path"`
	PlaylistDir string `json:"-"`

	// Runtime state
	ViewerCount int `json:"-"`
	PID         int `json:"-"`
}

// Telemetry holds real-time stats for a worker process.
type Telemetry struct {
	CPU     float64 `json:"cpu"`
	MemMB   float64 `json:"mem_mb"`
	Frame   int64   `json:"frame"`
	FPS     float64 `json:"fps"`
	Bitrate float64 `json:"bitrate_kbps"`
	Speed   float64 `json:"speed"`
}

// Store is the central, thread-safe in-memory state store.
type Store struct {
	mu         sync.RWMutex
	inputs     map[string]*Input      // keyed by stream_path
	outputs    map[string]*Output     // keyed by stream_path/output_id
	recordings map[string]*Recording  // keyed by stream_path
	hls        map[string]*HLSSession // keyed by stream_path
	telemetry  map[int]*Telemetry     // keyed by PID

	// Callback invoked after any state mutation for auto-save.
	OnChange func()
}

// NewStore creates a new empty state store.
func NewStore() *Store {
	return &Store{
		inputs:     make(map[string]*Input),
		outputs:    make(map[string]*Output),
		recordings: make(map[string]*Recording),
		hls:        make(map[string]*HLSSession),
		telemetry:  make(map[int]*Telemetry),
	}
}

// --- Input operations ---

func (s *Store) AddInput(in *Input) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.inputs[in.StreamPath]; exists {
		return fmt.Errorf("input %q already exists", in.StreamPath)
	}
	s.inputs[in.StreamPath] = in
	s.changed()
	return nil
}

func (s *Store) RemoveInput(streamPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.inputs[streamPath]; !exists {
		return fmt.Errorf("input %q not found", streamPath)
	}
	delete(s.inputs, streamPath)
	s.changed()
	return nil
}

func (s *Store) GetInput(streamPath string) (*Input, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	in, ok := s.inputs[streamPath]
	return in, ok
}

func (s *Store) ListInputs() []*Input {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Input, 0, len(s.inputs))
	for _, in := range s.inputs {
		result = append(result, in)
	}
	return result
}

func (s *Store) UpdateInputStatus(streamPath string, status InputStatus, lastErr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in, ok := s.inputs[streamPath]; ok {
		in.Status = status
		in.LastError = lastErr
	}
}

func (s *Store) UpdateInputRemoteAddr(streamPath, remoteAddr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in, ok := s.inputs[streamPath]; ok {
		in.RemoteAddr = remoteAddr
	}
}

// --- Output operations ---

func outputKey(streamPath, outputID string) string {
	return streamPath + "/" + outputID
}

func (s *Store) AddOutput(out *Output) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := outputKey(out.StreamPath, out.OutputID)
	if _, exists := s.outputs[key]; exists {
		return fmt.Errorf("output %q already exists", key)
	}
	s.outputs[key] = out
	s.changed()
	return nil
}

func (s *Store) RemoveOutput(streamPath, outputID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := outputKey(streamPath, outputID)
	if _, exists := s.outputs[key]; !exists {
		return fmt.Errorf("output %q not found", key)
	}
	delete(s.outputs, key)
	s.changed()
	return nil
}

func (s *Store) GetOutput(streamPath, outputID string) (*Output, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out, ok := s.outputs[outputKey(streamPath, outputID)]
	return out, ok
}

func (s *Store) ListOutputsForInput(streamPath string) []*Output {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Output
	for _, out := range s.outputs {
		if out.StreamPath == streamPath {
			result = append(result, out)
		}
	}
	return result
}

func (s *Store) ListOutputs() []*Output {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Output, 0, len(s.outputs))
	for _, out := range s.outputs {
		result = append(result, out)
	}
	return result
}

func (s *Store) UpdateOutputStatus(streamPath, outputID string, status OutputStatus, lastErr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if out, ok := s.outputs[outputKey(streamPath, outputID)]; ok {
		out.Status = status
		out.LastError = lastErr
	}
}

// --- Recording operations ---

func (s *Store) AddRecording(rec *Recording) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.recordings[rec.StreamPath]; exists {
		return fmt.Errorf("recording for %q already active", rec.StreamPath)
	}
	s.recordings[rec.StreamPath] = rec
	s.changed()
	return nil
}

func (s *Store) RemoveRecording(streamPath string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.recordings[streamPath]; !exists {
		return fmt.Errorf("recording for %q not found", streamPath)
	}
	delete(s.recordings, streamPath)
	s.changed()
	return nil
}

func (s *Store) GetRecording(streamPath string) (*Recording, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.recordings[streamPath]
	return rec, ok
}

func (s *Store) ListRecordings() []*Recording {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Recording, 0, len(s.recordings))
	for _, rec := range s.recordings {
		result = append(result, rec)
	}
	return result
}

// --- HLS operations ---

func (s *Store) AddHLSSession(sess *HLSSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hls[sess.StreamPath] = sess
}

func (s *Store) RemoveHLSSession(streamPath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.hls, streamPath)
}

func (s *Store) GetHLSSession(streamPath string) (*HLSSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.hls[streamPath]
	return sess, ok
}

func (s *Store) UpdateHLSViewerCount(streamPath string, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.hls[streamPath]; ok {
		sess.ViewerCount = count
	}
}

// --- Telemetry operations ---

func (s *Store) UpdateTelemetry(pid int, t *Telemetry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.telemetry[pid] = t
}

func (s *Store) GetTelemetry(pid int) (*Telemetry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.telemetry[pid]
	return t, ok
}

func (s *Store) RemoveTelemetry(pid int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.telemetry, pid)
}

// --- Persistence ---

// Snapshot represents the persistable state for config.json.
type Snapshot struct {
	Inputs  []*Input  `json:"inputs"`
	Outputs []*Output `json:"outputs"`
}

// TakeSnapshot creates a serializable copy of current persistent state.
func (s *Store) TakeSnapshot() *Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := &Snapshot{
		Inputs:  make([]*Input, 0, len(s.inputs)),
		Outputs: make([]*Output, 0, len(s.outputs)),
	}
	for _, in := range s.inputs {
		snap.Inputs = append(snap.Inputs, in)
	}
	for _, out := range s.outputs {
		snap.Outputs = append(snap.Outputs, out)
	}
	return snap
}

// LoadSnapshot loads state from a snapshot (used by --resume).
func (s *Store) LoadSnapshot(snap *Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inputs = make(map[string]*Input, len(snap.Inputs))
	for _, in := range snap.Inputs {
		in.Status = InputStatusStopped
		s.inputs[in.StreamPath] = in
	}
	s.outputs = make(map[string]*Output, len(snap.Outputs))
	for _, out := range snap.Outputs {
		out.Status = OutputStatusStopped
		s.outputs[outputKey(out.StreamPath, out.OutputID)] = out
	}
}

// SaveToFile atomically writes state snapshot to a JSON file.
func (s *Store) SaveToFile(path string) error {
	snap := s.TakeSnapshot()
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename to final: %w", err)
	}
	return nil
}

// LoadFromFile loads a snapshot from a JSON file.
func LoadFromFile(path string) (*Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read state file: %w", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("unmarshal state file: %w", err)
	}
	return &snap, nil
}

func (s *Store) changed() {
	if s.OnChange != nil {
		go s.OnChange()
	}
}

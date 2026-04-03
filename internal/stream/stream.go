package stream

import (
	"sync"
	"time"
)

type PipelineStreamStatus int

const (
	PStreamStopped PipelineStreamStatus = iota
	PStreamStarting
	PStreamRunning
	PStreamError
)

func (s PipelineStreamStatus) String() string {
	switch s {
	case PStreamStopped:
		return "Stopped"
	case PStreamStarting:
		return "Starting"
	case PStreamRunning:
		return "Running"
	case PStreamError:
		return "Error"
	default:
		return "Unknown"
	}
}

type PipelineStreamType string

const (
	PTypeInput     PipelineStreamType = "input"
	PTypeOutput    PipelineStreamType = "output"
	PTypeRecording PipelineStreamType = "recording"
	PTypeHLS       PipelineStreamType = "hls"
)

type PipelineStream struct {
	Name      string
	Type      PipelineStreamType
	SourceURL string
	LocalURL  string
	Status    PipelineStreamStatus
	LastError string
	Proc      FFmpegProcess
	Preset    string

	RefCount int

	CreatedAt time.Time
	StartedAt time.Time
}

func NewPipelineStream(name string, streamType PipelineStreamType, sourceURL string) *PipelineStream {
	return &PipelineStream{
		Name:      name,
		Type:      streamType,
		SourceURL: sourceURL,
		Status:    PStreamStopped,
		CreatedAt: time.Now(),
	}
}

func (s *PipelineStream) IncrementRef() {
	s.RefCount++
}

func (s *PipelineStream) DecrementRef() {
	if s.RefCount > 0 {
		s.RefCount--
	}
}

func (s *PipelineStream) CanStop() bool {
	return s.RefCount <= 0
}

type PipelineHLSSession struct {
	PipelineStream
	Mu         sync.RWMutex
	Dir        string
	Ready      bool
	ViewerIDs  map[string]time.Time
	LastAccess time.Time
}

func NewPipelineHLSSession(name string, sourceURL string) *PipelineHLSSession {
	return &PipelineHLSSession{
		PipelineStream: *NewPipelineStream(name, PTypeHLS, sourceURL),
		ViewerIDs:      make(map[string]time.Time),
		LastAccess:     time.Now(),
	}
}

type PipelineRecording struct {
	Name      string        `json:"name"`
	SourceURL string        `json:"source_url"`
	Filename  string        `json:"filename"`
	FilePath  string        `json:"file_path"`
	FileSize  int64         `json:"file_size"`
	StartedAt time.Time     `json:"started_at"`
	StoppedAt time.Time     `json:"stopped_at"`
	Active    bool          `json:"active"`
	Proc      FFmpegProcess `json:"-"`
}

type Relay struct {
	Input      *PipelineStream
	Outputs    map[string]*PipelineStream
	Recording  *PipelineRecording
	HLSSession *PipelineHLSSession
}

type RelayStatus struct {
	Input   RelayInputStatus    `json:"input"`
	Outputs []RelayOutputStatus `json:"outputs"`
}

type RelayInputStatus struct {
	InputName       string  `json:"input_name"`
	InputURL        string  `json:"input_url"`
	LocalURL        string  `json:"local_url"`
	Status          string  `json:"status"`
	LastError       string  `json:"last_error,omitempty"`
	RefCount        int     `json:"ref_count"`
	Speed           float64 `json:"speed"`
	CPU             float64 `json:"cpu"`
	Mem             uint64  `json:"mem"`
	RecordingActive bool    `json:"recording_active"`
}

type RelayOutputStatus struct {
	OutputName string  `json:"output_name"`
	Status     string  `json:"status"`
	LastError  string  `json:"last_error,omitempty"`
	Preset     string  `json:"preset,omitempty"`
	Bitrate    float64 `json:"bitrate"`
	CPU        float64 `json:"cpu"`
	Mem        uint64  `json:"mem"`
}

type PipelineServerStatus struct {
	CPU float64 `json:"cpu"`
	Mem uint64  `json:"mem"`
}

type StatusResponse struct {
	Server PipelineServerStatus `json:"server"`
	Relays []RelayStatus        `json:"relays"`
}

type RecordingListItem struct {
	Name      string    `json:"name"`
	Source    string    `json:"source"`
	Filename  string    `json:"filename"`
	StartedAt time.Time `json:"started_at"`
	FileSize  int64     `json:"file_size"`
	Active    bool      `json:"active"`
}

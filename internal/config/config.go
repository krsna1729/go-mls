// Package config provides configuration management for the go-mls application
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"go-mls/internal/logger"
)

// Config represents the main application configuration
type Config struct {
	// HTTP server configuration
	HTTP HTTPConfig `json:"http"`

	// Relay configuration
	Relay RelayConfig `json:"relay"`

	// Recording configuration
	Recording RecordingConfig `json:"recording"`

	// Logging configuration
	Logging LoggingConfig `json:"logging"`

	// HLS configuration
	HLS HLSConfig `json:"hls"`

	// FFmpeg configuration
	FFmpeg FFmpegConfig `json:"ffmpeg"`
}

// FFmpegConfig contains ffmpeg path and loglevel
// Used for all ffmpeg invocations (relay, hls, recording, etc)
type FFmpegConfig struct {
	Path     string `json:"path"`
	LogLevel string `json:"loglevel"`
}

// HTTPConfig contains HTTP server settings
// All durations use time.Duration with json:",string" tag for human-readable JSON
type HTTPConfig struct {
	Host         string   `json:"host"`
	Port         string   `json:"port"`
	ReadTimeout  Duration `json:"read_timeout"`
	WriteTimeout Duration `json:"write_timeout"`
	IdleTimeout  Duration `json:"idle_timeout"`
}

// RelayConfig contains relay timeouts and RTSP server config
// All durations use time.Duration with json:",string" tag for human-readable JSON
type RelayConfig struct {
	InputTimeout  Duration   `json:"input_timeout"`
	OutputTimeout Duration   `json:"output_timeout"`
	RTSPServer    RTSPConfig `json:"rtsp_server"`
}

// RTSPConfig contains RTSP server settings
type RTSPConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

// RecordingConfig contains recording-specific settings
type RecordingConfig struct {
	Directory string `json:"directory"`
}

// LoggingConfig contains logging settings
type LoggingConfig struct {
	Level string `json:"level"`
	File  string `json:"file,omitempty"`
}

// HLSConfig contains HLS manager settings
// All durations use time.Duration with json:",string" tag for human-readable JSON
type HLSConfig struct {
	CleanupInterval        Duration `json:"cleanup_interval"`
	SessionTimeout         Duration `json:"session_timeout"`
	FailedCooldown         Duration `json:"failed_cooldown"`
	NotFoundLogInterval    Duration `json:"not_found_log_interval"`
	PlaylistReadyTimeout   Duration `json:"playlist_ready_timeout"`
	PlaylistPollInterval   Duration `json:"playlist_poll_interval"`
	PlaylistPollAttempts   int      `json:"playlist_poll_attempts"`
	ViewerHeartbeatTimeout Duration `json:"viewer_heartbeat_timeout"`
	FFmpegStopTimeout      Duration `json:"ffmpeg_stop_timeout"`
	PlaylistBaseDir        string   `json:"playlist_base_dir"`
	SegmentDuration        Duration `json:"segment_duration"`
	PlaylistSize           int      `json:"playlist_size"`
	FFmpegPreset           string   `json:"ffmpeg_preset"`
}

// https://gist.github.com/ulexxander/a678baa2ae3454f9516a1cd7450ed6be
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err != nil {
		return err
	}
	val, err := time.ParseDuration(str)
	*d = Duration(val)
	return err
}

func (d Duration) String() string {
	return time.Duration(d).String()
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		HTTP: HTTPConfig{
			Host:         "0.0.0.0",
			Port:         "8080",
			ReadTimeout:  Duration(30 * time.Second),
			WriteTimeout: Duration(30 * time.Second),
			IdleTimeout:  Duration(120 * time.Second),
		},
		Relay: RelayConfig{
			InputTimeout:  Duration(30 * time.Second),
			OutputTimeout: Duration(60 * time.Second),
			RTSPServer: RTSPConfig{
				Host: "127.0.0.1",
				Port: 8554,
			},
		},
		Recording: RecordingConfig{
			Directory: "recordings",
		},
		Logging: LoggingConfig{
			Level: "info",
			File:  "",
		},
		HLS: HLSConfig{
			CleanupInterval:        Duration(2 * time.Minute),
			SessionTimeout:         Duration(5 * time.Minute),
			FailedCooldown:         Duration(30 * time.Second),
			NotFoundLogInterval:    Duration(10 * time.Second),
			PlaylistReadyTimeout:   Duration(10 * time.Second),
			PlaylistPollInterval:   Duration(200 * time.Millisecond),
			PlaylistPollAttempts:   50,
			ViewerHeartbeatTimeout: Duration(30 * time.Second),
			FFmpegStopTimeout:      Duration(2 * time.Second),
			PlaylistBaseDir:        "/tmp",
			SegmentDuration:        Duration(2 * time.Second),
			PlaylistSize:           6,
			FFmpegPreset:           "ultrafast",
		},
		FFmpeg: FFmpegConfig{
			Path:     "ffmpeg",
			LogLevel: "info",
		},
	}
}

// LoadConfig loads configuration from a file, falling back to defaults if the file doesn't exist
// Uses the provided logger for important events and errors
func LoadConfig(filename string, log *logger.Logger) (*Config, error) {
	config := DefaultConfig()

	// If file doesn't exist, return defaults
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		log.Info("Using default configuration")
		return config, nil
	}

	log.Info("Loading configuration from", "filename", filename)

	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return config, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	// Validate HTTP configuration
	if c.HTTP.Port == "" {
		return fmt.Errorf("HTTP port cannot be empty")
	}

	// Validate relay timeouts
	if c.Relay.InputTimeout <= 0 {
		return fmt.Errorf("input timeout must be positive")
	}

	if c.Relay.OutputTimeout <= c.Relay.InputTimeout {
		return fmt.Errorf("output timeout must be greater than input timeout")
	}

	// Validate RTSP server configuration
	if c.Relay.RTSPServer.Port <= 0 || c.Relay.RTSPServer.Port > 65535 {
		return fmt.Errorf("RTSP server port must be between 1 and 65535")
	}

	// Validate recording directory
	if c.Recording.Directory == "" {
		return fmt.Errorf("recording directory cannot be empty")
	}

	return nil
}

// GetRTSPServerURL returns the full RTSP server URL
func (c *Config) GetRTSPServerURL() string {
	return fmt.Sprintf("rtsp://%s:%d", c.Relay.RTSPServer.Host, c.Relay.RTSPServer.Port)
}

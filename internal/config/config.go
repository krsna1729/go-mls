// Package config provides configuration management for the go-mls application
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
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
}

// HTTPConfig contains HTTP server settings
type HTTPConfig struct {
	Host         string        `json:"host"`
	Port         string        `json:"port"`
	ReadTimeout  time.Duration `json:"read_timeout"`
	WriteTimeout time.Duration `json:"write_timeout"`
	IdleTimeout  time.Duration `json:"idle_timeout"`
}

// RelayConfig contains relay-specific settings
type RelayConfig struct {
	InputTimeout  time.Duration `json:"input_timeout"`
	OutputTimeout time.Duration `json:"output_timeout"`
	RTSPServer    RTSPConfig    `json:"rtsp_server"`
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

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		HTTP: HTTPConfig{
			Host:         "0.0.0.0",
			Port:         "8080",
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 30 * time.Second,
			IdleTimeout:  120 * time.Second,
		},
		Relay: RelayConfig{
			InputTimeout:  30 * time.Second,
			OutputTimeout: 60 * time.Second,
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
		},
	}
}

// LoadConfig loads configuration from a file, falling back to defaults if the file doesn't exist
func LoadConfig(filename string) (*Config, error) {
	config := DefaultConfig()

	// If file doesn't exist, return defaults
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return config, nil
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %v", err)
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %v", err)
	}

	return config, nil
}

// SaveConfig saves the configuration to a file
func (c *Config) SaveConfig(filename string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %v", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %v", err)
	}

	return nil
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

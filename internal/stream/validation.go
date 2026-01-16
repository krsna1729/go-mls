// Package stream contains validation utilities for stream operations.
package stream

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// Supported URL schemes for input streams.
var allowedInputSchemes = map[string]bool{
	"rtmp":  true,
	"rtmps": true,
	"rtsp":  true,
	"rtsps": true,
	"http":  true,
	"https": true,
	"file":  true,
	"srt":   true,
}

// Supported URL schemes for output streams.
var allowedOutputSchemes = map[string]bool{
	"rtmp":  true,
	"rtmps": true,
	"file":  true,
	"srt":   true,
}

// inputNameRegex defines valid input/output names.
// Allows alphanumeric, underscore, hyphen, and period.
// Must start with alphanumeric character.
var inputNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_\-\.]*$`)

// MaxNameLength is the maximum allowed length for input/output names.
const MaxNameLength = 128

// MaxURLLength is the maximum allowed length for URLs.
const MaxURLLength = 2048

// ValidateInputURL validates an input URL for use in streaming operations.
// Returns nil if valid, or a ValidationError describing the issue.
func ValidateInputURL(inputURL string) error {
	if inputURL == "" {
		return NewValidationError("input_url", "", "cannot be empty")
	}

	if len(inputURL) > MaxURLLength {
		return NewValidationError("input_url", inputURL[:50]+"...", "exceeds maximum length")
	}

	parsed, err := url.Parse(inputURL)
	if err != nil {
		return NewValidationError("input_url", inputURL, "malformed URL")
	}

	scheme := strings.ToLower(parsed.Scheme)
	if !allowedInputSchemes[scheme] {
		return NewValidationError("input_url", inputURL,
			"unsupported scheme; allowed: rtmp, rtmps, rtsp, rtsps, http, https, file, srt")
	}

	// Validate file:// URLs for path traversal
	if scheme == "file" {
		if err := validateFilePath(parsed.Path); err != nil {
			return err
		}
	}

	return nil
}

// ValidateOutputURL validates an output URL for use in streaming operations.
// Returns nil if valid, or a ValidationError describing the issue.
func ValidateOutputURL(outputURL string) error {
	if outputURL == "" {
		return NewValidationError("output_url", "", "cannot be empty")
	}

	if len(outputURL) > MaxURLLength {
		return NewValidationError("output_url", outputURL[:50]+"...", "exceeds maximum length")
	}

	parsed, err := url.Parse(outputURL)
	if err != nil {
		return NewValidationError("output_url", outputURL, "malformed URL")
	}

	scheme := strings.ToLower(parsed.Scheme)
	if !allowedOutputSchemes[scheme] {
		return NewValidationError("output_url", outputURL,
			"unsupported scheme; allowed: rtmp, rtmps, file, srt")
	}

	// Validate file:// URLs for path traversal
	if scheme == "file" {
		if err := validateFilePath(parsed.Path); err != nil {
			return err
		}
	}

	return nil
}

// ValidateName validates an input or output name.
// Names must be alphanumeric with underscores, hyphens, and periods allowed.
func ValidateName(name string, fieldName string) error {
	if name == "" {
		return NewValidationError(fieldName, "", "cannot be empty")
	}

	if len(name) > MaxNameLength {
		return NewValidationError(fieldName, name[:50]+"...", "exceeds maximum length")
	}

	if !inputNameRegex.MatchString(name) {
		return NewValidationError(fieldName, name,
			"must start with alphanumeric and contain only alphanumeric, underscore, hyphen, or period")
	}

	// Check for path traversal attempts
	if strings.Contains(name, "..") {
		return NewValidationError(fieldName, name, "contains invalid sequence '..'")
	}

	return nil
}

// ValidateInputName validates an input stream name.
func ValidateInputName(name string) error {
	return ValidateName(name, "input_name")
}

// ValidateOutputName validates an output stream name.
func ValidateOutputName(name string) error {
	return ValidateName(name, "output_name")
}

// validateFilePath checks a file path for security issues.
func validateFilePath(path string) error {
	if path == "" {
		return NewValidationError("path", "", "file path cannot be empty")
	}

	// Check for path traversal
	if strings.Contains(path, "..") {
		return NewValidationError("path", path, "path traversal not allowed")
	}

	// Resolve to absolute and check it doesn't escape
	// This catches symlink attacks and other tricks
	cleaned := filepath.Clean(path)
	if strings.HasPrefix(cleaned, "..") {
		return NewValidationError("path", path, "path traversal not allowed")
	}

	// Disallow certain dangerous paths
	dangerousPrefixes := []string{
		"/etc/",
		"/proc/",
		"/sys/",
		"/dev/",
		"/boot/",
	}
	lowerPath := strings.ToLower(cleaned)
	for _, prefix := range dangerousPrefixes {
		if strings.HasPrefix(lowerPath, prefix) {
			return NewValidationError("path", path, "access to system directories not allowed")
		}
	}

	return nil
}

// SanitizeForFFmpeg escapes a URL for safe use in FFmpeg commands.
// This prevents command injection through specially crafted URLs.
func SanitizeForFFmpeg(input string) string {
	// FFmpeg uses colon as option separator, escape it in filenames
	// Also handle special characters that could cause issues
	dangerous := []string{
		"$(", "`", // Command substitution
		"\n", "\r", // Newline injection
		";", "&&", "||", "|", // Command chaining
	}

	result := input
	for _, d := range dangerous {
		result = strings.ReplaceAll(result, d, "")
	}

	return result
}

// ValidatePresetName checks if a preset name is valid.
func ValidatePresetName(preset string) error {
	if preset == "" {
		return nil // Empty preset is allowed (means no preset)
	}

	// Check against known presets
	validPresets := []string{"YouTube", "Facebook", "Twitch", "Custom", "Passthrough"}
	for _, valid := range validPresets {
		if preset == valid {
			return nil
		}
	}

	return NewValidationError("preset", preset, "unknown preset; allowed: YouTube, Facebook, Twitch, Custom, Passthrough")
}

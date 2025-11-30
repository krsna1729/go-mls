package errors

import (
	"fmt"
	"net/http"
	"testing"
)

func TestInvalidInput(t *testing.T) {
	err := InvalidInput("missing field")
	if err.Code != ErrInvalidInput {
		t.Errorf("Expected code %s, got %s", ErrInvalidInput, err.Code)
	}
	if err.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status %d, got %d", http.StatusBadRequest, err.StatusCode)
	}
	if err.Message != "missing field" {
		t.Errorf("Expected message 'missing field', got '%s'", err.Message)
	}
}

func TestInvalidInputWithDetails(t *testing.T) {
	err := InvalidInputWithDetails("validation failed", "field 'name' is required")
	if err.Details != "field 'name' is required" {
		t.Errorf("Expected details 'field 'name' is required', got '%s'", err.Details)
	}
}

func TestNotFound(t *testing.T) {
	err := NotFound("relay")
	if err.Code != ErrResourceNotFound {
		t.Errorf("Expected code %s, got %s", ErrResourceNotFound, err.Code)
	}
	if err.StatusCode != http.StatusNotFound {
		t.Errorf("Expected status %d, got %d", http.StatusNotFound, err.StatusCode)
	}
	if err.Message != "relay not found" {
		t.Errorf("Expected message 'relay not found', got '%s'", err.Message)
	}
}

func TestConflict(t *testing.T) {
	err := Conflict("relay already exists")
	if err.Code != ErrConflict {
		t.Errorf("Expected code %s, got %s", ErrConflict, err.Code)
	}
	if err.StatusCode != http.StatusConflict {
		t.Errorf("Expected status %d, got %d", http.StatusConflict, err.StatusCode)
	}
}

func TestInternal(t *testing.T) {
	err := Internal("database connection failed")
	if err.Code != ErrInternal {
		t.Errorf("Expected code %s, got %s", ErrInternal, err.Code)
	}
	if err.StatusCode != http.StatusInternalServerError {
		t.Errorf("Expected status %d, got %d", http.StatusInternalServerError, err.StatusCode)
	}
}

func TestTimeout(t *testing.T) {
	err := Timeout("request timed out")
	if err.Code != ErrTimeout {
		t.Errorf("Expected code %s, got %s", ErrTimeout, err.Code)
	}
	if err.StatusCode != http.StatusRequestTimeout {
		t.Errorf("Expected status %d, got %d", http.StatusRequestTimeout, err.StatusCode)
	}
}

func TestUnauthorized(t *testing.T) {
	err := Unauthorized("invalid credentials")
	if err.Code != ErrUnauthorized {
		t.Errorf("Expected code %s, got %s", ErrUnauthorized, err.Code)
	}
	if err.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected status %d, got %d", http.StatusUnauthorized, err.StatusCode)
	}
}

func TestForbidden(t *testing.T) {
	err := Forbidden("access denied")
	if err.Code != ErrForbidden {
		t.Errorf("Expected code %s, got %s", ErrForbidden, err.Code)
	}
	if err.StatusCode != http.StatusForbidden {
		t.Errorf("Expected status %d, got %d", http.StatusForbidden, err.StatusCode)
	}
}

func TestErrorString(t *testing.T) {
	err := InvalidInput("test error")
	expected := "invalid_input: test error"
	if err.Error() != expected {
		t.Errorf("Expected error string '%s', got '%s'", expected, err.Error())
	}

	errWithDetails := InvalidInputWithDetails("test error", "additional info")
	expectedWithDetails := "invalid_input: test error (additional info)"
	if errWithDetails.Error() != expectedWithDetails {
		t.Errorf("Expected error string '%s', got '%s'", expectedWithDetails, errWithDetails.Error())
	}
}

func TestFromError(t *testing.T) {
	// Test nil error
	if FromError(nil) != nil {
		t.Error("Expected nil for nil error")
	}

	// Test AppError passthrough
	appErr := InvalidInput("test")
	result := FromError(appErr)
	if result != appErr {
		t.Error("Expected same AppError to be returned")
	}

	// Test standard error conversion
	stdErr := fmt.Errorf("standard error")
	result = FromError(stdErr)
	if result.Code != ErrInternal {
		t.Errorf("Expected code %s, got %s", ErrInternal, result.Code)
	}
	if result.Message != "standard error" {
		t.Errorf("Expected message 'standard error', got '%s'", result.Message)
	}
}

// Package errors provides structured error types for consistent API error responses
package errors

import (
	"fmt"
	"net/http"
)

// ErrorCode represents a standardized error code
type ErrorCode string

const (
	ErrInvalidInput     ErrorCode = "invalid_input"
	ErrResourceNotFound ErrorCode = "not_found"
	ErrConflict         ErrorCode = "conflict"
	ErrInternal         ErrorCode = "internal_error"
	ErrTimeout          ErrorCode = "timeout"
	ErrUnauthorized     ErrorCode = "unauthorized"
	ErrForbidden        ErrorCode = "forbidden"
)

// AppError represents a structured application error
type AppError struct {
	Code       ErrorCode `json:"code"`
	Message    string    `json:"message"`
	Details    string    `json:"details,omitempty"`
	StatusCode int       `json:"-"`
}

// Error implements the error interface
func (e *AppError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, e.Details)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// InvalidInput creates an error for invalid input
func InvalidInput(msg string) *AppError {
	return &AppError{
		Code:       ErrInvalidInput,
		Message:    msg,
		StatusCode: http.StatusBadRequest,
	}
}

// InvalidInputWithDetails creates an error for invalid input with additional details
func InvalidInputWithDetails(msg, details string) *AppError {
	return &AppError{
		Code:       ErrInvalidInput,
		Message:    msg,
		Details:    details,
		StatusCode: http.StatusBadRequest,
	}
}

// NotFound creates an error for resource not found
func NotFound(resource string) *AppError {
	return &AppError{
		Code:       ErrResourceNotFound,
		Message:    fmt.Sprintf("%s not found", resource),
		StatusCode: http.StatusNotFound,
	}
}

// NotFoundWithDetails creates an error for resource not found with additional details
func NotFoundWithDetails(resource, details string) *AppError {
	return &AppError{
		Code:       ErrResourceNotFound,
		Message:    fmt.Sprintf("%s not found", resource),
		Details:    details,
		StatusCode: http.StatusNotFound,
	}
}

// Conflict creates an error for resource conflicts
func Conflict(msg string) *AppError {
	return &AppError{
		Code:       ErrConflict,
		Message:    msg,
		StatusCode: http.StatusConflict,
	}
}

// ConflictWithDetails creates an error for resource conflicts with additional details
func ConflictWithDetails(msg, details string) *AppError {
	return &AppError{
		Code:       ErrConflict,
		Message:    msg,
		Details:    details,
		StatusCode: http.StatusConflict,
	}
}

// Internal creates an error for internal server errors
func Internal(msg string) *AppError {
	return &AppError{
		Code:       ErrInternal,
		Message:    msg,
		StatusCode: http.StatusInternalServerError,
	}
}

// InternalWithDetails creates an error for internal server errors with additional details
func InternalWithDetails(msg, details string) *AppError {
	return &AppError{
		Code:       ErrInternal,
		Message:    msg,
		Details:    details,
		StatusCode: http.StatusInternalServerError,
	}
}

// Timeout creates an error for timeout scenarios
func Timeout(msg string) *AppError {
	return &AppError{
		Code:       ErrTimeout,
		Message:    msg,
		StatusCode: http.StatusRequestTimeout,
	}
}

// Unauthorized creates an error for unauthorized access
func Unauthorized(msg string) *AppError {
	return &AppError{
		Code:       ErrUnauthorized,
		Message:    msg,
		StatusCode: http.StatusUnauthorized,
	}
}

// Forbidden creates an error for forbidden access
func Forbidden(msg string) *AppError {
	return &AppError{
		Code:       ErrForbidden,
		Message:    msg,
		StatusCode: http.StatusForbidden,
	}
}

// FromError converts a standard error to an AppError
// If the error is already an AppError, it returns it unchanged
// Otherwise, it wraps it as an internal error
func FromError(err error) *AppError {
	if err == nil {
		return nil
	}
	if appErr, ok := err.(*AppError); ok {
		return appErr
	}
	return Internal(err.Error())
}

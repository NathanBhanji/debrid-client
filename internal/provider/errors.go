package provider

import (
	"errors"
	"fmt"
	"time"
)

// ErrorKind classifies provider failures so the engine can decide whether to
// retry, back off, surface, or give up.
type ErrorKind string

const (
	// ErrAuth: bad/expired credentials. Not retryable; needs user action.
	ErrAuth ErrorKind = "auth"
	// ErrRateLimited: slow down. Retry after RetryAfter (or a default backoff).
	ErrRateLimited ErrorKind = "rate_limited"
	// ErrNotFound: the torrent/link no longer exists at the provider.
	ErrNotFound ErrorKind = "not_found"
	// ErrLimit: account limits hit (active torrents, quota, fair use). Retry later.
	ErrLimit ErrorKind = "limit"
	// ErrPermanent: the request can never succeed (invalid magnet, infringing, unsupported).
	ErrPermanent ErrorKind = "permanent"
	// ErrTransient: network/5xx/unknown. Retry with backoff.
	ErrTransient ErrorKind = "transient"
)

// Error is a classified provider failure.
type Error struct {
	Kind       ErrorKind
	Code       string        // provider-specific code if any (e.g. "MAGNET_INVALID_URI", "33")
	Message    string        // human-readable detail
	HTTPStatus int           // 0 if not applicable
	RetryAfter time.Duration // hint for ErrRateLimited/ErrLimit (0 = unknown)
	Err        error         // wrapped cause
}

func (e *Error) Error() string {
	msg := string(e.Kind)
	if e.Code != "" {
		msg += " " + e.Code
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	if e.HTTPStatus != 0 {
		msg += fmt.Sprintf(" (http %d)", e.HTTPStatus)
	}
	return "provider: " + msg
}

func (e *Error) Unwrap() error { return e.Err }

// Is lets errors.Is(err, &Error{Kind: X}) match on kind alone.
func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return t.Kind == e.Kind && (t.Code == "" || t.Code == e.Code)
}

// Retryable reports whether the operation may succeed if retried later.
func (e *Error) Retryable() bool {
	switch e.Kind {
	case ErrRateLimited, ErrLimit, ErrTransient:
		return true
	}
	return false
}

// Errorf builds an Error.
func Errorf(kind ErrorKind, code, format string, args ...any) *Error {
	return &Error{Kind: kind, Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap classifies an underlying error.
func Wrap(kind ErrorKind, err error) *Error {
	if err == nil {
		return nil
	}
	return &Error{Kind: kind, Message: err.Error(), Err: err}
}

// KindOf returns the ErrorKind of err, or ErrTransient for unclassified errors
// (the safe default: retry with backoff).
func KindOf(err error) ErrorKind {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Kind
	}
	return ErrTransient
}

// IsRetryable reports whether err should be retried.
func IsRetryable(err error) bool {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Retryable()
	}
	return true
}

// RetryAfter returns the provider's hint for when to retry, if any.
func RetryAfter(err error) time.Duration {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.RetryAfter
	}
	return 0
}

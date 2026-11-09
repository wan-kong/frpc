package domain

import "errors"

type ErrorCode string

const (
	Validation        ErrorCode = "validation"
	NotFound          ErrorCode = "not_found"
	StorageFailed     ErrorCode = "storage_failed"
	Canceled          ErrorCode = "canceled"
	Timeout           ErrorCode = "timeout"
	Conflict          ErrorCode = "conflict"
	SecretUnavailable ErrorCode = "secret_unavailable"
	BinaryMissing     ErrorCode = "binary_missing"
	PortInUse         ErrorCode = "port_in_use"
	StartFailed       ErrorCode = "start_failed"
	AdminUnavailable  ErrorCode = "admin_unavailable"
	ApplyFailed       ErrorCode = "apply_failed"
	DownloadFailed    ErrorCode = "download_failed"
	ChecksumMismatch  ErrorCode = "checksum_mismatch"
	PermissionDenied  ErrorCode = "permission_denied"
)

type Error struct {
	Code      ErrorCode
	Message   string
	FieldPath string
	Retryable bool
	Cause     error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Cause }
func Failure(code ErrorCode, message, field string, retry bool, cause error) error {
	return &Error{Code: code, Message: message, FieldPath: field, Retryable: retry, Cause: cause}
}
func ErrorInfo(err error) (*Error, bool) {
	var info *Error
	ok := errors.As(err, &info)
	return info, ok
}

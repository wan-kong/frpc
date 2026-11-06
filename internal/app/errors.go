package app

import (
	"context"
	"errors"
	"os"
	"syscall"

	"frp-desktop/internal/domain"
)

// commandError classifies facts, preserving more specific errors from lower layers.
func commandError(err error, fallback domain.ErrorCode) error {
	if err == nil {
		return nil
	}
	if _, ok := domain.ErrorInfo(err); ok {
		return err
	}
	code, retry := fallback, false
	switch {
	case errors.Is(err, context.Canceled):
		code = domain.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		code, retry = domain.Timeout, true
	case errors.Is(err, os.ErrPermission):
		code = domain.PermissionDenied
	case errors.Is(err, syscall.EADDRINUSE):
		code = domain.PortInUse
	}
	return domain.Failure(code, err.Error(), "", retry, err)
}

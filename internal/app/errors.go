package app

import (
	"context"
	"errors"
	"os"

	"frp-desktop/internal/domain"
	"frp-desktop/internal/platform"
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
	case platform.IsAddrInUse(err):
		code = domain.PortInUse
	}
	return domain.Failure(code, err.Error(), "", retry, err)
}

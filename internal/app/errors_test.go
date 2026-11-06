package app

import (
	"context"
	"errors"
	"frp-desktop/internal/domain"
	"os"
	"syscall"
	"testing"
)

func TestCommandErrorsPreserveFactsAndSpecificCauses(t *testing.T) {
	specific := domain.Failure(domain.Conflict, "changed", "revision", false, nil)
	if got := commandError(specific, domain.ApplyFailed); got != specific {
		t.Fatal("specific field info lost")
	}
	for _, c := range []struct {
		err  error
		code domain.ErrorCode
	}{
		{context.Canceled, domain.Canceled}, {context.DeadlineExceeded, domain.Timeout}, {os.ErrPermission, domain.PermissionDenied}, {syscall.EADDRINUSE, domain.PortInUse}, {errors.New("I/O failure"), domain.StorageFailed},
	} {
		got := commandError(c.err, domain.StorageFailed)
		info, ok := domain.ErrorInfo(got)
		if !ok || info.Code != c.code || !errors.Is(got, c.err) {
			t.Fatal(c.code, got)
		}
	}
	if commandError(nil, domain.ApplyFailed) != nil {
		t.Fatal("success became failure")
	}
}

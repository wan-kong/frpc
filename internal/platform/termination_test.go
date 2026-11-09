package platform

import "testing"

func TestOnlyExplicitSystemReasonsSkipConfirmation(t *testing.T) {
	for _, reason := range []uint32{0, 0x71756974, 0x71756961} {
		if IsSystemQuitReason(reason) {
			t.Fatalf("ordinary quit %x treated as shutdown", reason)
		}
	}
	for _, reason := range []uint32{0x73687574, 0x72657374, 0x726c676f} {
		if !IsSystemQuitReason(reason) {
			t.Fatalf("system reason %x not recognized", reason)
		}
	}
}

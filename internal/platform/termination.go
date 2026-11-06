package platform

// Apple Event quit reasons from the macOS AERegistry.h public SDK.
func IsSystemQuitReason(reason uint32) bool {
	switch reason {
	case 0x73687574, 0x72657374, 0x726c676f, 0x6c6f676f:
		return true
	}
	return false
}

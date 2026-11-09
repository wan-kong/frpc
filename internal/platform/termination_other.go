//go:build !darwin

package platform

func SystemTerminationRequested() bool { return false }

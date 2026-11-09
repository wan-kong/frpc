//go:build darwin

package platform

import "github.com/ebitengine/purego/objc"

// SystemTerminationRequested runs on the UI thread inside OnBeforeQuit.
// A Dock/menu quit has no shutdown/logout reason and therefore remains interactive.
func SystemTerminationRequested() bool {
	manager := objc.ID(objc.GetClass("NSAppleEventManager")).Send(objc.RegisterName("sharedAppleEventManager"))
	event := manager.Send(objc.RegisterName("currentAppleEvent"))
	if event == 0 {
		return false
	}
	reason := event.Send(objc.RegisterName("paramDescriptorForKeyword:"), uint32(0x7768793f))
	if reason == 0 {
		return false
	}
	return IsSystemQuitReason(uint32(reason.Send(objc.RegisterName("enumCodeValue"))))
}

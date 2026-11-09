package ui

// QuitNeedsConfirmation keeps user quits distinct from explicit system shutdown.
func QuitNeedsConfirmation(running int, dirty, system, confirmRunning, relaunch bool) bool {
	return !system && (dirty || running > 0 && (confirmRunning || relaunch))
}

package ui

func (a *App) coreChoice(id string) string {
	co, ok := a.State.Core(id)
	if !ok {
		return ""
	}
	switch {
	case co.Bundled:
		return "frpc " + co.Version + "（内置）"
	case co.Certified:
		return "frpc " + co.Version + "（已校验）"
	default:
		suffix := co.ID
		if len(suffix) > 6 {
			suffix = suffix[len(suffix)-6:]
		}
		return "frpc " + co.Version + "（自定义 " + suffix + "）"
	}
}
func (a *App) coreChoiceID(choice string) string {
	for _, co := range a.State.Cores {
		if a.coreChoice(co.ID) == choice {
			return co.ID
		}
	}
	return ""
}

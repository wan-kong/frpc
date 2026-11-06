package ui

func (a *App) coreChoice(id string) string {
	co, ok := a.State.Core(id)
	if !ok {
		return ""
	}
	source := "自定义"
	if co.Certified {
		source = "已认证"
	}
	if co.Bundled {
		source = "离线保底"
	}
	suffix := co.ID
	if len(suffix) > 6 {
		suffix = suffix[len(suffix)-6:]
	}
	return "frpc " + co.Version + " · " + source + " · " + co.Arch + " · " + suffix
}
func (a *App) coreChoiceID(choice string) string {
	for _, co := range a.State.Cores {
		if a.coreChoice(co.ID) == choice {
			return co.ID
		}
	}
	return ""
}

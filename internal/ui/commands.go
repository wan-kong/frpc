package ui

import (
	"context"

	"frp-desktop/internal/app"
	"frp-desktop/internal/domain"
)

func profileCommand(p domain.Profile, action app.Action) app.Command {
	return app.Command{Meta: app.CommandMeta{OperationID: domain.NewID(), ProfileID: p.ID, ExpectedRevision: p.Revision}, Action: action}
}
func saveIntent(p domain.Profile, values map[string]string, save bool, after string) app.Command {
	if !save {
		return profileCommand(p, app.ApplyProfile{ConnectIfStopped: after == "connect"})
	}
	action := app.SaveProfileDraft{Profile: p, Values: values, RunAfterSave: after}
	if p.Revision == 0 {
		return profileCommand(p, app.CreateProfile(action))
	}
	return profileCommand(p, action)
}
func (a *App) RunProfile(label string, p domain.Profile, action app.Action) {
	cmd := profileCommand(p, action)
	a.Do(label, func(ctx context.Context) error { _, err := a.Controller.Execute(ctx, cmd); return err }, nil)
}

func toggleIntent(p domain.Profile) app.Command {
	items := map[string]bool{}
	for _, x := range p.Tunnels {
		items[x.ID] = x.Enabled
	}
	for _, v := range p.Visitors {
		items[v.ID] = v.Enabled
	}
	return profileCommand(p, app.SetTunnelEnabled{Items: items})
}

func (a *App) RunProfiles(label string, profiles []domain.Profile, action app.Action) {
	commands := make([]app.Command, 0, len(profiles))
	for _, p := range profiles {
		commands = append(commands, profileCommand(p, action))
	}
	a.Do(label, func(ctx context.Context) error { _, err := a.Controller.ExecuteMany(ctx, commands); return err }, nil)
}

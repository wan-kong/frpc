package app

import (
	"context"
	"frp-desktop/internal/domain"
)

func (c *Controller) profileCommands(ids []string, action Action) []Command {
	state := c.Store.Snapshot()
	commands := make([]Command, 0, len(ids))
	for _, id := range ids {
		p, _ := state.Profile(id)
		commands = append(commands, Command{Meta: CommandMeta{OperationID: domain.NewID(), ProfileID: id, ExpectedRevision: p.Revision}, Action: action})
	}
	return commands
}
func (c *Controller) StartMany(ctx context.Context, ids []string) error {
	_, err := c.ExecuteMany(ctx, c.profileCommands(ids, StartProfile{}))
	return err
}
func (c *Controller) StopMany(ids []string) error {
	_, err := c.ExecuteMany(context.Background(), c.profileCommands(ids, StopProfile{}))
	return err
}

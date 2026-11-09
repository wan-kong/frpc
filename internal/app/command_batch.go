package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ExecuteMany runs a collection of already captured commands, with at most four
// independent profiles progressing at once. Retrying the same collection keeps
// every child's operation ID and does not execute it again.
func (c *Controller) ExecuteMany(ctx context.Context, commands []Command) ([]CommandResult, error) {
	results := make([]CommandResult, len(commands))
	errs := make([]error, len(commands))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(4, len(commands)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				result, err := c.Execute(ctx, commands[i])
				results[i] = result
				if err != nil {
					errs[i] = fmt.Errorf("连接 %s：%w", commands[i].Meta.ProfileID, err)
				}
			}
		}()
	}
	canceled := false
	for i := range commands {
		if ctx.Err() != nil {
			errs[i] = ctx.Err()
			break
		}
		select {
		case jobs <- i:
		case <-ctx.Done():
			errs[i] = ctx.Err()
			canceled = true
		}
		if canceled {
			break
		}
	}
	close(jobs)
	wg.Wait()
	return results, errors.Join(errs...)
}

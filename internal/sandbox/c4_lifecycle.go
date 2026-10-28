package sandbox

import (
	"context"
	"foundry/internal/domain"
	"io"
	"time"
)

// c4Lifecycle is backend-neutral and private. Production wrappers establish
// provenance before entering the shared durable state machine.
type c4Lifecycle interface {
	executionTimeout() time.Duration
	create(context.Context) (string, error)
	start(context.Context, string) error
	wait(context.Context, string) error
	inspect(context.Context, string) (C4Inspection, error)
	logs(context.Context, string, io.Writer) (bool, error)
	remove(context.Context, string) error
}

type c4CommandLifecycle struct {
	plan   C4Plan
	driver C4Driver
}

func (d c4CommandLifecycle) executionTimeout() time.Duration {
	return time.Duration(domain.FixedC4Policy().TimeoutSeconds) * time.Second
}

func (d c4CommandLifecycle) create(ctx context.Context) (string, error) {
	return d.driver.Create(ctx, d.plan.Create())
}
func (d c4CommandLifecycle) start(ctx context.Context, id string) error {
	c, e := d.plan.Command("start", id)
	if e != nil {
		return e
	}
	return d.driver.Start(ctx, c)
}
func (d c4CommandLifecycle) wait(ctx context.Context, id string) error {
	c, e := d.plan.Command("wait", id)
	if e != nil {
		return e
	}
	return d.driver.Wait(ctx, c)
}
func (d c4CommandLifecycle) inspect(ctx context.Context, id string) (C4Inspection, error) {
	c, e := d.plan.Command("inspect", id)
	if e != nil {
		return C4Inspection{}, e
	}
	return d.driver.Inspect(ctx, c)
}
func (d c4CommandLifecycle) logs(ctx context.Context, id string, w io.Writer) (bool, error) {
	c, e := d.plan.Command("logs", id)
	if e != nil {
		return false, e
	}
	return d.driver.Logs(ctx, c, w)
}
func (d c4CommandLifecycle) remove(ctx context.Context, id string) error {
	c, e := d.plan.Command("remove", id)
	if e != nil {
		return e
	}
	return d.driver.Remove(ctx, c)
}

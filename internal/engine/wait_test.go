package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitIdleCapturesCompletionWithoutChangingAuthority(t *testing.T) {
	finished := make(chan struct{})
	op := &operation{finished: finished}
	e := &Engine{active: op}
	done := make(chan error, 1)
	go func() { done <- e.WaitIdle(context.Background()) }()
	select {
	case err := <-done:
		t.Fatal("returned before completion", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(finished)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("completion not observed")
	}
	if e.active != op || e.closed {
		t.Fatal("barrier changed engine state")
	}
}
func TestWaitIdleNilAndCancellation(t *testing.T) {
	e := &Engine{}
	if err := e.WaitIdle(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(e.WaitIdle(ctx), context.Canceled) {
		t.Fatal("cancellation ignored")
	}
	e.active = &operation{finished: make(chan struct{})}
	if !errors.Is(e.WaitIdle(ctx), context.Canceled) {
		t.Fatal("active cancellation ignored")
	}
}

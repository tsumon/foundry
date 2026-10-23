package vmruntime

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestSuspendedOwnershipFailureRetainsHandlesAndRetries(t *testing.T) {
	for _, stage := range []string{"assign", "cancel", "resume"} {
		for _, failure := range []string{"terminate", "wait-error", "wait-timeout"} {
			t.Run(stage+"/"+failure, func(t *testing.T) {
				calls := []string{}
				closed := []uintptr{}
				canTerminate, signaled := false, false
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				api := ownedProcessCalls{
					assign: func(job, process uintptr) error {
						if job != 22 || process != 11 {
							t.Fatal("wrong exact handles")
						}
						calls = append(calls, "assign")
						if stage == "assign" {
							return errors.New("assign refused")
						}
						if stage == "cancel" {
							cancel()
						}
						return nil
					},
					resume: func(thread uintptr) error {
						calls = append(calls, "resume")
						if thread != 33 {
							t.Fatal("wrong thread")
						}
						return errors.New("resume failed")
					},
					terminateProcess: func(handle uintptr) error {
						if handle != 11 {
							t.Fatal("wrong process")
						}
						calls = append(calls, "terminate-process")
						if failure == "terminate" && !canTerminate {
							return errors.New("termination denied")
						}
						return nil
					},
					terminateJob: func(handle uintptr) error {
						if handle != 22 {
							t.Fatal("wrong job")
						}
						calls = append(calls, "terminate-job")
						if failure == "terminate" && !canTerminate {
							return errors.New("termination denied")
						}
						return nil
					},
					wait: func(handle uintptr, millis uint32) (bool, error) {
						if handle != 11 {
							t.Fatal("wrong process observed")
						}
						if signaled {
							return true, nil
						}
						if failure == "wait-error" {
							return false, errors.New("wait failed")
						}
						time.Sleep(time.Millisecond)
						return false, nil
					},
					exitCode: func(uintptr) (uint32, error) { return 1, nil }, close: func(handle uintptr) error { closed = append(closed, handle); return nil },
				}
				owner := &ownedProcess{process: 11, job: 22, calls: api}
				if e := owner.admit(ctx, 33); e == nil {
					t.Fatal("failed admission accepted")
				}
				if len(closed) != 0 {
					t.Fatal("native ownership released during failed admission")
				}
				if stage != "resume" && reflect.DeepEqual(calls, []string{"assign", "resume"}) {
					t.Fatal("resumed before successful admission")
				}
				stopErr := owner.stop()
				if failure == "terminate" && stopErr == nil {
					t.Fatal("termination failure hidden")
				}
				want := "terminate-job"
				if stage == "assign" {
					want = "terminate-process"
				}
				if calls[len(calls)-1] != want {
					t.Fatal("wrong termination authority", calls)
				}
				short, stop := context.WithTimeout(context.Background(), 5*time.Millisecond)
				defer stop()
				if owner.waitForTermination(short) == nil {
					t.Fatal("unconfirmed process accepted")
				}
				if owner.terminatedState() || len(closed) != 0 {
					t.Fatal("unknown termination discarded handles")
				}
				canTerminate, signaled = true, true
				if e := owner.stop(); e != nil {
					t.Fatal(e)
				}
				_ = owner.waitForTermination(context.Background())
				if !owner.terminatedState() || !reflect.DeepEqual(closed, []uintptr{11, 22}) {
					t.Fatalf("positive termination did not close exact owner: %v", closed)
				}
			})
		}
	}
}

func TestSuspendedOwnershipAssignsBeforeResume(t *testing.T) {
	calls := []string{}
	owner := &ownedProcess{process: 11, job: 22, calls: ownedProcessCalls{assign: func(uintptr, uintptr) error { calls = append(calls, "assign"); return nil }, resume: func(uintptr) error { calls = append(calls, "resume"); return nil }}}
	if e := owner.admit(context.Background(), 33); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(calls, []string{"assign", "resume"}) {
		t.Fatal("guest resumed before job assignment", calls)
	}
}

package main

import (
	"errors"
	"testing"
)

type failedServeFixture struct {
	done  chan struct{}
	fail  bool
	calls int
}

func (f *failedServeFixture) Origin() string        { return "" }
func (f *failedServeFixture) Done() <-chan struct{} { return f.done }
func (f *failedServeFixture) Close() error {
	f.calls++
	if f.fail {
		return errors.New("controlled cleanup failure")
	}
	select {
	case <-f.done:
	default:
		close(f.done)
	}
	return nil
}

func TestFailedServeCustodySurvivesRepeatedCleanupFailure(t *testing.T) {
	if !retryFailedServeLifetimes() {
		t.Fatal("unrelated pending lifetime")
	}
	f := &failedServeFixture{done: make(chan struct{}), fail: true}
	if finishServe(f) == nil || retryFailedServeLifetimes() {
		t.Fatal("unresolved native owner abandoned")
	}
	if f.calls != 2 {
		t.Fatal("cleanup not retried once")
	}
	f.fail = false
	if !retryFailedServeLifetimes() {
		t.Fatal("recovered owner not released")
	}
	select {
	case <-f.done:
	default:
		t.Fatal("owner still active")
	}
	if !retryFailedServeLifetimes() || f.calls != 3 {
		t.Fatal("released lifetime retried")
	}
}

func TestCompletedServeDiagnosticDoesNotRetainReleasedOwner(t *testing.T) {
	f := &failedServeFixture{done: make(chan struct{}), fail: true}
	close(f.done)
	if finishServe(f) == nil {
		t.Fatal("completed transport diagnostic suppressed")
	}
	if !retryFailedServeLifetimes() || f.calls != 1 {
		t.Fatal("released owner fenced later startup")
	}
}

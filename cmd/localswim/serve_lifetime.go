package main

import "sync"

type serveRuntime interface {
	Origin() string
	Done() <-chan struct{}
	Close() error
}

// A failed in-process serve call must not abandon an owned lifetime. The normal
// executable has one invocation; its kernel handles end with that process.
// Embedded callers retain unresolved owners here until a later explicit serve
// invocation retries cleanup. This never retries initialization or a command.
var failedServeLifetimes struct {
	sync.Mutex
	owners   []serveRuntime
	retrying bool
}

func finishServe(r serveRuntime) error {
	if r == nil {
		return nil
	}
	err := r.Close()
	if err == nil {
		return nil
	}
	select {
	case <-r.Done(): // A completed lifetime can retain a transport diagnostic.
		return err
	default:
	}
	failedServeLifetimes.Lock()
	defer failedServeLifetimes.Unlock()
	for _, owner := range failedServeLifetimes.owners {
		if owner == r {
			return err
		}
	}
	failedServeLifetimes.owners = append(failedServeLifetimes.owners, r)
	return err
}

func retryFailedServeLifetimes() bool {
	failedServeLifetimes.Lock()
	if failedServeLifetimes.retrying {
		failedServeLifetimes.Unlock()
		return false
	}
	failedServeLifetimes.retrying = true
	owners := failedServeLifetimes.owners
	failedServeLifetimes.owners = nil
	failedServeLifetimes.Unlock()
	// Close may perform native IO or drain. No bookkeeping lock spans it.
	for _, owner := range owners {
		_ = finishServe(owner)
	}
	failedServeLifetimes.Lock()
	defer failedServeLifetimes.Unlock()
	failedServeLifetimes.retrying = false
	return len(failedServeLifetimes.owners) == 0
}

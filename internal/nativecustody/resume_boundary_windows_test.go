//go:build windows && amd64 && nativecustodyqualification

package nativecustody

import (
	"context"
	"testing"
	"time"
)

func TestCancellationAfterPolicyQueryCannotResume(t *testing.T) {
	for _, cause := range []string{"stop", "deadline"} {
		t.Run(cause, func(t *testing.T) {
			s := fixtureSpec(t, "normal")
			entered, release := make(chan struct{}), make(chan struct{})
			s.beforeResumeBoundary = func() { close(entered); <-release }
			if cause == "deadline" {
				s.deadline = time.Now().Add(8 * time.Second)
			}
			o := &owner{}
			p := launch(t, o, s)
			select {
			case <-entered:
			case <-time.After(10 * time.Second):
				t.Fatal("real policy query boundary not reached")
			}
			if cause == "stop" {
				if err := o.stop(p); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-p.ctx.Done():
			case <-time.After(10 * time.Second):
				t.Fatal("operation cancellation missing")
			}
			close(release)
			r := joined(t, p)
			want := "CANCELLED"
			if cause == "deadline" {
				want = "TIMED_OUT"
			}
			if p.resumeAttempted || r.Code != want || len(r.Stdout) != 0 || len(r.Stderr) != 0 {
				t.Fatal("cancelled boundary resumed or exposed output", p.resumeAttempted, r.Code)
			}
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			next, err := o.start(cancelled, s)
			if err != nil {
				t.Fatal("joined owner remained busy", err)
			}
			<-next.done
		})
	}
}

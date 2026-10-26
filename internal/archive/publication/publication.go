// Package publication owns separate offline native restore publication and status.
// Publication never activates a board, enrolls trust or creates human credentials.
package publication

import (
	"context"
	"time"

	A "github.com/localswim/localswim/internal/contract/archive"
)

// Request contains local-only paths. ProtectedRoots must include every live board
// and source tree known by the trusted CLI composition. Paths never enter a
// receipt, response, diagnostic or archive. Source directory overlap is also
// excluded independently of ProtectedRoots.
type Request struct {
	SourceArchive  string
	Destination    string
	RequestID      A.UUID
	ProtectedRoots []string
}
type Response struct {
	Status A.Status
	Exit   int
}

func conclusion(outcome A.RestoreOutcome, r *A.Receipt) Response {
	s := A.Status{APIVersion: 1, Scope: "restore", Outcome: outcome}
	if r != nil {
		s.Result = A.RestoreResult{SourceIdentity: &r.SourceManifest.Source, SourceRevision: &r.SourceManifest.Revision, Destination: &r.Destination}
	}
	exit := 4
	if outcome == "confirmed" {
		exit = 0
	}
	if outcome == "failed" {
		exit = 1
	}
	return Response{Status: s, Exit: exit}
}

// Restore performs one attempt only. Every interrupted or ambiguous publication
// remains unknown and retains its candidate; callers must not automatically retry.
func Restore(ctx context.Context, request Request) Response {
	return nativeRestore(ctx, request, time.Now)
}

// Status searches only the exact final and request-qualified partial paths. It
// never mutates a receipt, repairs a database, replays or resumes construction.
func Status(ctx context.Context, destination string, requestID A.UUID) Response {
	return nativeStatus(ctx, destination, requestID)
}

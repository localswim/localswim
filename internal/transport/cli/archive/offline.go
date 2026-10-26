package archive

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"

	"github.com/localswim/localswim/internal/archive/publication"
	A "github.com/localswim/localswim/internal/contract/archive"
)

func requestID() (A.UUID, error) {
	var raw [16]byte
	if _, e := rand.Read(raw[:]); e != nil {
		return "", errLocal
	}
	raw[6] = raw[6]&15 | 64
	raw[8] = raw[8]&63 | 128
	return A.UUID(fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:])), nil
}
func offline(ctx context.Context, q invocation, roots []string, stderr io.Writer) result {
	var response publication.Response
	if q.kind == "restore status" {
		response = publication.Status(ctx, q.destination, A.UUID(q.request))
	} else {
		// A missing protected-tree inventory is not permission to create an
		// unbounded destination. Composition must explicitly provide its roots.
		if roots == nil {
			return result{value: A.Status{APIVersion: 1, Scope: "restore", Outcome: "unknown"}, exit: 4}
		}
		id, e := requestID()
		if e != nil {
			return result{value: A.Status{APIVersion: 1, Scope: "restore", Outcome: "unknown"}, exit: 4}
		}
		if stderr == nil {
			return result{value: A.Status{APIVersion: 1, Scope: "restore", Outcome: "failed"}, exit: 1}
		}
		line := "RESTORE_REQUEST " + string(id) + "\n"
		if n, e := io.WriteString(stderr, line); e != nil || n != len(line) {
			return result{value: A.Status{APIVersion: 1, Scope: "restore", Outcome: "failed"}, exit: 1}
		}
		response = publication.Restore(ctx, publication.Request{SourceArchive: q.source, Destination: q.destination, RequestID: id, ProtectedRoots: append([]string{}, roots...)})
	}
	return result{value: response.Status, exit: response.Exit, localComplete: response.Exit == 0}
}

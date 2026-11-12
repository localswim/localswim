package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

// Entity validators belong to HTTP representations. Domain records retain only revisions.
func (a attachment) entityTag() string { return etag(a.ID, a.Revision) }

func (s serviceStatus) entityTag() string { return etag(s.ServiceInstanceID, s.revision) }

func (e entity) entityTag() string { return etag(e.ID, e.Revision) }

func (l lane) entityTag() string { return etag(l.ID, l.Revision) }

func (c card) entityTag() string { return etag(c.ID, c.Revision) }

func (c comment) entityTag() string { return etag(c.ID, 1) }

func etag(id string, revision uint64) string { return fmt.Sprintf("\"%s-%d\"", id, revision) }

func (r relationship) entityTag() string { return etag(r.ID, r.Revision) }

func (w workspace) entityTag() string { return etag(w.identity, w.Revision) }

func workspaceHTTPView(w workspace, hidden func(string) bool) (workspace, string) {
	if hidden == nil || !slices.ContainsFunc(w.PinnedBoardIDs, hidden) {
		return w, w.entityTag()
	}
	w.PinnedBoardIDs = slices.DeleteFunc(slices.Clone(w.PinnedBoardIDs), hidden)
	// Use visible IDs to bind the filtered tag to its startup projection.
	digest := sha256.Sum256([]byte(strings.Join(w.PinnedBoardIDs, ",")))
	return w, etag(w.identity+"-pins-"+hex.EncodeToString(digest[:16]), w.Revision)
}

func snapshotRepresentationTag(state snapshot) string {
	data, _ := marshalCanonical(state)
	return fmt.Sprintf("\"%x\"", sha256.Sum256(data))
}

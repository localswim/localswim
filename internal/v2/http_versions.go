package v2

import (
	"crypto/sha256"
	"fmt"
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

func snapshotRepresentationTag(state snapshot) string {
	data, _ := marshalCanonical(state)
	return fmt.Sprintf("\"%x\"", sha256.Sum256(data))
}

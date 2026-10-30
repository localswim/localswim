package contract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// ResolvedRoute is populated by the server adapter from its registered method,
// route and validated board/grant lookup, never from a client actor or command.
// Its validity does not establish permission, incarnation or revision admission.
type ResolvedRoute struct {
	Command  Command
	BoardID  string
	TargetID string
}

// AuthenticatedActor contains only the ID derived by the authentication layer.
// No bearer, login code, origin, selector or native provider enters an Intent.
type AuthenticatedActor struct{ ActorID string }

// Intent is immutable; byte access returns copies. It is semantic evidence, not
// proof of authorization, transaction admission, dispatch or a terminal outcome.
type Intent struct {
	canonical   []byte
	fingerprint string
	command     Command
}

func (i Intent) CanonicalBytes() []byte { return bytes.Clone(i.canonical) }
func (i Intent) Fingerprint() string    { return i.fingerprint }
func (i Intent) Command() Command       { return i.command }

// Freeze validates a wire request and binds the independently resolved route and
// authenticated actor before normalization and hashing. State-dependent checks
// (including automation rights, workflow and revisions) belong to admission.
func Freeze(wire []byte, route ResolvedRoute, actor AuthenticatedActor) (Intent, error) {
	if route.Command == "attachment.add" && len(wire) > 16384 {
		return Intent{}, fmt.Errorf("attachment metadata exceeds 16 KiB")
	}
	d, e := descriptor(route.Command)
	if e != nil {
		return Intent{}, e
	}
	if e = uuid.validate(route.BoardID, "route.boardId"); e != nil {
		return Intent{}, e
	}
	if e = uuid.validate(actor.ActorID, "authenticated.actorId"); e != nil {
		return Intent{}, e
	}
	v, e := DecodeJSON(wire)
	if e != nil {
		return Intent{}, e
	}
	if e = RequestType(d).validate(v, "request"); e != nil {
		return Intent{}, e
	}
	m := v.(map[string]any)
	if b, ok := m["boardId"]; ok && b != route.BoardID {
		return Intent{}, fmt.Errorf("route board mismatch")
	}
	if d.Target == "none" {
		if route.TargetID != "" {
			return Intent{}, fmt.Errorf("unexpected route target")
		}
	} else {
		if e = uuid.validate(route.TargetID, "route.target"); e != nil {
			return Intent{}, e
		}
		key := "cardId"
		if d.Target == "grant" {
			key = "grantId"
		}
		if m["target"].(map[string]any)[key] != route.TargetID {
			return Intent{}, fmt.Errorf("route target mismatch")
		}
	}
	payload := m["input"].(map[string]any)
	// Owner/archive omission retains current facts. Neither new optional field
	// may change legacy canonical bytes; explicit false is an unarchive intent.
	for k, f := range d.Input.Fields {
		if _, ok := payload[k]; !ok && f.Optional && k != "ownerId" && k != "archived" && k != "parent" {
			payload[k] = nil
		}
	}
	var p any = payload
	if d.Recovery {
		p = map[string]any{"input": payload, "previewHash": m["previewHash"]}
	}
	semantic := map[string]any{"fingerprintVersion": json.Number("1"), "boardId": route.BoardID, "incarnation": m["incarnation"], "opId": m["opId"], "actorId": actor.ActorID, "command": string(route.Command), "target": m["target"], "payload": p, "expectedRevision": m["expectedRevision"], "expectedCardRevision": m["expectedCardRevision"]}
	b, e := Canonical(semantic)
	if e != nil {
		return Intent{}, e
	}
	h := sha256.Sum256(b)
	return Intent{canonical: b, fingerprint: hex.EncodeToString(h[:]), command: route.Command}, nil
}

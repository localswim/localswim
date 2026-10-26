package contract

import "fmt"

func nullable(t *Type) *Type { c := *t; c.Nullable = true; return &c }

// ResultType describes the accepted D02 retained COMMITTED result object, not a
// secret credential delivery response or the entire HTTP outcome envelope.
func ResultType(command Command) *Type {
	f := map[string]Field{"cardId": req(uuid)}
	switch command {
	case "card.create", "card.edit", "review-handoff":
	case "comment.add":
		f["commentId"] = req(uuid)
	case "attachment.add":
		f["attachmentId"] = req(uuid)
	case "link.add", "link.remove":
		f["linkId"] = req(uuid)
	case "approve":
		f["approvalId"] = req(uuid)
	case "grant.create", "grant.revoke":
		f = map[string]Field{"grantId": req(uuid)}
	case "history.clear", "retention.set":
		f = map[string]Field{"retentionVersion": req(number(1, MaxInteger)), "coverageRevision": req(number(1, MaxInteger))}
	default:
		return nil
	}
	return cloneType(obj(f))
}
func RefusalResultType() *Type {
	errorType := obj(map[string]Field{"code": req(text("")), "message": req(text("")), "field": req(nullable(text("")))})
	conflict := obj(map[string]Field{"incarnation": req(uuid), "revision": req(revision), "cardRevision": req(nullable(revision)), "cardId": req(nullable(uuid))})
	return cloneType(obj(map[string]Field{"error": req(errorType), "conflict": req(nullable(conflict))}))
}

// ValidateResult validates exact retained result shape; it does not establish
// that the referenced effect occurred, exists, or belongs to the operation.
func ValidateResult(data []byte, command Command, committed bool) error {
	t := RefusalResultType()
	if committed {
		t = ResultType(command)
		if t == nil {
			return fmt.Errorf("unsupported command")
		}
	} else {
		if _, e := descriptor(command); e != nil {
			return e
		}
	}
	v, e := DecodeJSON(data)
	if e != nil {
		return e
	}
	return t.validate(v, "result")
}

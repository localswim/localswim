package transport

import (
	"fmt"
	"reflect"

	A "github.com/localswim/localswim/internal/contract/archive"
)

const (
	NoticeResponseLimit = 128 << 10
	NoticeRequestLimit  = 1024
	NoticeInboxLimit    = 64
	NoticeGapLimit      = 64
	NoticeEventLimit    = 1000
)

type NoticeScope string
type NoticePhase string
type NoticeReason string
type NoticeClass string
type NoticeLookupState string

type NoticeResponse struct {
	APIVersion A.Version   `json:"apiVersion"`
	Scope      NoticeScope `json:"scope"`
	Result     NoticeView  `json:"result"`
}

type NoticeView struct {
	Identity          A.Identity    `json:"identity"`
	ControlGeneration UUIDv4        `json:"controlGeneration"`
	TargetID          *UUIDv4       `json:"targetId"`
	State             NoticePhase   `json:"state"`
	Reason            *NoticeReason `json:"reason"`
	Checkpoint        *A.Number     `json:"checkpoint"`
	GapCount          A.Number      `json:"gapCount"`
	Last              *NoticeCut    `json:"last"`
	Pending           *NoticeCut    `json:"pending"`
	Entries           []NoticePair  `json:"entries"`
	Lookup            *NoticeLookup `json:"lookup"`
}

type NoticeCut struct {
	Class      NoticeClass `json:"class"`
	LowerEvent A.Number    `json:"lowerEvent"`
	UpperEvent *A.Number   `json:"upperEvent"`
	EventCount *A.Number   `json:"eventCount"`
	DeliveryID *A.Digest   `json:"deliveryId"`
}

type NoticePair struct {
	TargetID   UUIDv4      `json:"targetId"`
	DeliveryID A.Digest    `json:"deliveryId"`
	AcceptedAt A.Timestamp `json:"acceptedAt"`
	Class      NoticeClass `json:"class"`
	LowerEvent A.Number    `json:"lowerEvent"`
	UpperEvent *A.Number   `json:"upperEvent"`
	EventCount *A.Number   `json:"eventCount"`
}

type NoticeLookup struct {
	State   NoticeLookupState `json:"state"`
	Receipt *NoticePair       `json:"receipt"`
}

type NoticeControlRequest struct {
	Incarnation               A.UUID `json:"incarnation"`
	ExpectedControlGeneration UUIDv4 `json:"expectedControlGeneration"`
}

type NoticeLookupRequest struct {
	Incarnation A.UUID   `json:"incarnation"`
	TargetID    UUIDv4   `json:"targetId"`
	DeliveryID  A.Digest `json:"deliveryId"`
}

func noticeRange(class NoticeClass, lower A.Number, upper, count *A.Number) bool {
	if upper != nil && *upper < lower {
		return false
	}
	switch class {
	case "unavailable":
		return count == nil
	case "expired", "gapped":
		return upper != nil && count == nil
	case "observed-empty":
		return upper != nil && *upper == lower && count != nil && *count == 0
	default:
		return upper != nil && count != nil && *count > 0 && *count <= NoticeEventLimit &&
			*count == *upper-lower && (class != "mixed" || *count >= 2)
	}
}

func noticeByteLimit(t reflect.Type) int {
	switch t {
	case reflect.TypeFor[NoticeResponse]():
		return NoticeResponseLimit
	case reflect.TypeFor[NoticeControlRequest](), reflect.TypeFor[NoticeLookupRequest]():
		return NoticeRequestLimit
	default:
		return 0
	}
}

func noticeView(v NoticeView) error {
	if v.GapCount > NoticeGapLimit || len(v.Entries) > NoticeInboxLimit ||
		(v.TargetID == nil) != (v.Checkpoint == nil) ||
		v.State == "unconfigured" && v.TargetID != nil ||
		v.TargetID == nil && (v.GapCount != 0 || v.Last != nil || v.Pending != nil || len(v.Entries) != 0) ||
		v.Pending != nil && v.Pending.Class == "suppressed" {
		return fmt.Errorf("invalid notice view")
	}
	seen := map[A.Digest]bool{}
	for i, p := range v.Entries {
		if v.TargetID == nil || seen[p.DeliveryID] {
			return fmt.Errorf("invalid notice inbox identity")
		}
		seen[p.DeliveryID] = true
		if i > 0 {
			prev := v.Entries[i-1]
			if prev.AcceptedAt < p.AcceptedAt || prev.AcceptedAt == p.AcceptedAt && prev.DeliveryID > p.DeliveryID {
				return fmt.Errorf("invalid notice inbox order")
			}
		}
	}
	if v.Lookup != nil && v.Lookup.Receipt != nil && v.TargetID == nil {
		return fmt.Errorf("invalid notice receipt identity")
	}
	return nil
}

func noticeSchema(t reflect.Type, m map[string]any) {
	p := m["properties"].(map[string]any)
	switch t {
	case reflect.TypeFor[NoticeResponse]():
		m["x-max-utf8-bytes"] = NoticeResponseLimit
	case reflect.TypeFor[NoticeView]():
		p["entries"].(map[string]any)["maxItems"] = NoticeInboxLimit
		p["gapCount"].(map[string]any)["maximum"] = NoticeGapLimit
	case reflect.TypeFor[NoticeControlRequest](), reflect.TypeFor[NoticeLookupRequest]():
		m["x-max-utf8-bytes"] = NoticeRequestLimit
	}
}

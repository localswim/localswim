package transport

import (
	"encoding/json"
	A "github.com/localswim/localswim/internal/contract/archive"
	"testing"
)

func TestBoardAttentionStrictContextAndCounts(t *testing.T) {
	zero := A.Number(0)
	v := BoardAttention{APIVersion: 1, BoardID: "00000000-0000-4000-8000-000000000001", Incarnation: "00000000-0000-4000-8000-000000000002", AttentionContext: AttentionContext{PolicyVersion: 1, Profile: "default", Source: "implicit", Assignments: A.ResolveWorkflowAttention(nil), Lanes: []AttentionLane{{ID: "Ready for Review", Label: "Ready for Review", Role: "review"}}}, Attention: CatalogAttention{Blocked: CatalogCount{State: "not_configured"}, NeedsTerry: CatalogCount{State: "not_configured"}, ReadyForReview: CatalogCount{State: "known", Value: &zero}}}
	if err := Validate(v); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	for name, change := range map[string]func(*BoardAttention){
		"mixed version":   func(x *BoardAttention) { x.AttentionContext.PolicyVersion = 2 },
		"missing lane":    func(x *BoardAttention) { x.AttentionContext.Lanes = []AttentionLane{} },
		"unknown count":   func(x *BoardAttention) { x.Attention.ReadyForReview = CatalogCount{State: "unknown"} },
		"fabricated zero": func(x *BoardAttention) { x.Attention.Blocked = CatalogCount{State: "known", Value: &zero} },
		"wrong label":     func(x *BoardAttention) { x.AttentionContext.Lanes[0].Label = "Wrong" },
	} {
		t.Run(name, func(t *testing.T) {
			var x BoardAttention
			json.Unmarshal(raw, &x)
			change(&x)
			if Validate(x) == nil {
				t.Fatal("accepted contradictory attention")
			}
		})
	}
}

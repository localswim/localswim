package core

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	A "github.com/localswim/localswim/internal/contract/archive"
	T "github.com/localswim/localswim/internal/contract/transport"
)

func TestAttentionReadGrammarAndIdentity(t *testing.T) {
	q, code, exit := parse(args("attention", "show"))
	if code != "" || exit != 0 || q.method != "GET" || q.path != "/api/v1/boards/"+testBoard+"/attention" || q.id != "" {
		t.Fatal("attention read grammar")
	}
	for _, command := range [][]string{{"attention", "show", testActor}, {"attention", "show", "--input", "-"}, {"attention", "show", "--actor", testActor}, {"attention", "edit"}} {
		if _, code, _ := parse(args(command...)); code == "" {
			t.Fatal("invalid attention grammar admitted")
		}
	}
	zero := A.Number(0)
	value := T.BoardAttention{APIVersion: 1, BoardID: testBoard, Incarnation: testInc, Revision: 0,
		AttentionContext: T.AttentionContext{PolicyVersion: 1, Profile: "default", Source: "implicit", Assignments: A.ResolveWorkflowAttention(nil), Lanes: []T.AttentionLane{{ID: "Ready for Review", Label: "Ready for Review", Role: "review"}}},
		Attention:        T.CatalogAttention{ReadyForReview: T.CatalogCount{State: "known", Value: &zero}, Blocked: T.CatalogCount{State: "not_configured"}, NeedsTerry: T.CatalogCount{State: "not_configured"}}}
	for _, test := range []struct {
		name   string
		change func(*T.BoardAttention)
		exit   int
	}{{"valid", func(*T.BoardAttention) {}, 0}, {"foreign", func(v *T.BoardAttention) { v.Incarnation = testActor }, 2}, {"malformed", func(v *T.BoardAttention) { v.Attention.Blocked.Value = &zero }, 3}} {
		t.Run(test.name, func(t *testing.T) {
			v := value
			test.change(&v)
			wire, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			r := Runner{Provider: &provider{conn: connection(t, func(req *http.Request) (int, []byte, error) {
				if req.Method != "GET" || req.URL.Path != q.path {
					t.Fatal("incorrect attention request")
				}
				return 200, wire, nil
			})}}
			run(t, r, args("attention", "show"), nil, test.exit)
		})
	}
	text, ok := focusedRead(value)
	if !ok || !strings.Contains(text, "Ready for Review: 0 (known)") || !strings.Contains(text, "Blocked: Not configured") {
		t.Fatal("attention output loses known-zero distinction")
	}
}

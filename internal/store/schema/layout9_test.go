package schema

import "testing"

func TestFrozenNineWorkflowVocabulary(t *testing.T) {
	for _, raw := range []string{`{"attention":{"blocked":[],"needsTerry":[],"readyForReview":[]}}`, `{"attention":null}`, `{"future":true}`} {
		if validateV9WorkflowVocabulary([]byte(raw)) == nil {
			t.Fatal("new member accepted", raw)
		}
	}
	if err := validateV9WorkflowVocabulary([]byte(`{"profile":"default","workflowId":"synthetic","revision":1,"lanes":[],"transitions":[],"actorRights":{}}`)); err != nil {
		t.Fatal(err)
	}
}

package boardapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkflowBodyBoundBeforeDecode(t *testing.T) {
	for _, path := range []string{base + "/workflow", base + "/workflow/preview"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", path, strings.NewReader(strings.Repeat(" ", maxBody+1)))
		if _, ok := body(w, r); ok || w.Code != 413 {
			t.Fatal("oversize workflow body passed transport bound")
		}
	}
}

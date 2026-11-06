package attachments

import (
	T "github.com/localswim/localswim/internal/contract/transport"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFileReadHardCapBeforeSuccess(t *testing.T) {
	label := strings.Repeat("\x00", T.FileActorLabelLimit)
	w := httptest.NewRecorder()
	respond(w, 200, T.CardFileRead{Result: T.CardFileView{File: T.CardFile{Attribution: T.FileAttribution{ActorLabel: &label}}}})
	if w.Code != 503 || w.Body.Len() > 1024 || strings.Contains(w.Body.String(), "actorLabel") {
		t.Fatal("oversized success escaped", w.Code, w.Body.Len())
	}
}

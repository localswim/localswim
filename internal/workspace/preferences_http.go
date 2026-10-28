package workspace

import (
	T "github.com/localswim/localswim/internal/contract/transport"
	"io"
	"net/http"
)

func readPreferenceBody(w http.ResponseWriter, q *http.Request) (T.WorkspacePreferencesRequest, error) {
	b, e := io.ReadAll(http.MaxBytesReader(w, q.Body, PreferencesLimit))
	if e != nil {
		return T.WorkspacePreferencesRequest{}, ErrUnavailable
	}
	return T.Decode[T.WorkspacePreferencesRequest](b)
}

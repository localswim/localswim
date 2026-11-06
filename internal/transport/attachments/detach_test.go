package attachments

import (
	"net/http/httptest"
	"testing"
)

func TestDetachRouteIsExactAndPostOnly(t *testing.T) {
	path := "/api/v1/boards/00000000-0000-4000-8000-000000000001/cards/00000000-0000-4000-8000-000000000002/attachment-detach"
	for _, test := range []struct {
		method, path string
		status       int
	}{{"POST", path, 0}, {"GET", path, 405}, {"POST", path + "?x=1", 400}, {"POST", path + "?", 400}, {"POST", path + "/", 404}} {
		route, status := selectRoute(httptest.NewRequest(test.method, test.path, nil))
		if status != test.status {
			t.Fatalf("status %d want %d", status, test.status)
		}
		if status == 0 && (!route.detach || route.card != "00000000-0000-4000-8000-000000000002") {
			t.Fatal("wrong detach route")
		}
	}
}

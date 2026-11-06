package attachments

import (
	"net/http/httptest"
	"testing"
)

func TestFileReadRoutes(t *testing.T) {
	base := "https://example.localhost/api/v1/boards/10000000-0000-0000-0000-000000000001/cards/20000000-0000-0000-0000-000000000001/attachments"
	for _, test := range []struct {
		method, path string
		status       int
	}{{"GET", base, 0}, {"GET", base + "?limit=1", 0}, {"GET", base + "/30000000-0000-0000-0000-000000000001", 0}, {"GET", base + "/30000000-0000-0000-0000-000000000001?limit=1", 400}, {"POST", base + "?limit=1", 400}, {"GET", base + "/", 404}} {
		_, status := selectRoute(httptest.NewRequest(test.method, test.path, nil))
		if status != test.status {
			t.Fatal(test.path, status)
		}
	}
}

package assets

import (
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestEmbeddedInventoryAndCanonicalRoutes(t *testing.T) {
	if err := Verify(); err != nil {
		t.Fatal(err)
	}
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/", "/index.html", "/js/app.js", "/styles/app.css", "/js/api/hierarchy.js", "/js/board/hierarchy.js"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 200 {
			t.Fatalf("%s: %d", p, w.Code)
		}
		csp := w.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") {
			t.Fatal("unsafe CSP")
		}
	}
	for _, p := range []string{"/?", "/index.html?", "/js/app.js?", "/js/app", "/js/app.js?alias=1", "/?qualified=true", "/js/%61pp.js", "/private.json", "/js/app.js.map", "/js/../app.js", "/unknown"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		if w.Code != 404 {
			t.Fatalf("alias/private path %s: %d", p, w.Code)
		}
	}
}

func TestRuntimeRejectsMissingTamperedExtraAndManifest(t *testing.T) {
	original := fstest.MapFS{}
	if err := fs.WalkDir(embedded, "bundle", func(n string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		b, e := fs.ReadFile(embedded, n)
		original[n] = &fstest.MapFile{Data: b}
		return e
	}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"missing", "tampered", "extra", "manifest", "trailing"} {
		t.Run(kind, func(t *testing.T) {
			clone := fstest.MapFS{}
			for n, v := range original {
				clone[n] = &fstest.MapFile{Data: append([]byte(nil), v.Data...)}
			}
			switch kind {
			case "missing":
				delete(clone, "bundle/js/app.js")
			case "tampered":
				clone["bundle/js/app.js"].Data = []byte("tampered\n")
			case "extra":
				clone["bundle/private.json"] = &fstest.MapFile{Data: []byte("{}\n")}
			case "manifest":
				clone["bundle/manifest.json"].Data = []byte("{}\n")
			case "trailing":
				clone["bundle/manifest.json"].Data = append(clone["bundle/manifest.json"].Data, []byte("{}\n")...)
			}
			if _, err := verified(clone); err == nil {
				t.Fatal("invalid bundle accepted")
			}
		})
	}
}

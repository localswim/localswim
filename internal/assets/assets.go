// Package assets serves only the verified, declared production bundle.
package assets

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed bundle
var embedded embed.FS

type Manifest struct {
	Version  int               `json:"version"`
	Compiler string            `json:"compiler"`
	Inputs   map[string]string `json:"inputs"`
	Files    map[string]string `json:"files"`
}

func digest(b []byte) string { v := sha256.Sum256(b); return hex.EncodeToString(v[:]) }

// Verify checks the complete embedded inventory before any asset can be served.
func Verify() error { _, err := verified(embedded); return err }

func verified(source fs.FS) (map[string][]byte, error) {
	b, err := fs.ReadFile(source, "bundle/manifest.json")
	if err != nil {
		return nil, fmt.Errorf("assets: missing manifest: %w", err)
	}
	var m Manifest
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&m); err != nil || m.Version != 1 || m.Compiler != "7.0.2" || len(m.Inputs) == 0 || len(m.Files) != len(declared) {
		return nil, fmt.Errorf("assets: invalid manifest")
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("assets: trailing manifest data")
	}
	for name, value := range m.Inputs {
		decoded, err := hex.DecodeString(value)
		if !fs.ValidPath(name) || err != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("assets: invalid input binding")
		}
	}
	files := map[string][]byte{}
	err = fs.WalkDir(source, "bundle", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("assets: symlink")
		}
		if entry.IsDir() {
			return nil
		}
		if name == "bundle/manifest.json" {
			return nil
		}
		relative := strings.TrimPrefix(name, "bundle/")
		expected, ok := m.Files[relative]
		if !ok || !allowed(relative) {
			return fmt.Errorf("assets: undeclared asset %s", relative)
		}
		content, e := fs.ReadFile(source, name)
		if e != nil {
			return e
		}
		if digest(content) != expected {
			return fmt.Errorf("assets: hash mismatch %s", relative)
		}
		files[relative] = content
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) != len(m.Files) {
		return nil, fmt.Errorf("assets: missing asset")
	}
	if _, ok := files["index.html"]; !ok {
		return nil, fmt.Errorf("assets: missing entry")
	}
	return files, nil
}

var declared = map[string]bool{
	"index.html": true, "styles/app.css": true,
	"js/app.js": true, "js/api/boundary.js": true, "js/api/client.js": true, "js/api/schema-data.js": true, "js/api/w03-session.js": true,
	"js/archive-ui/view.js": true, "js/board/view.js": true, "js/recovery/indexeddb.js": true, "js/recovery/store.js": true, "js/recovery/types.js": true,
	"js/recovery-ui/admin.js": true, "js/recovery-ui/coordination.js": true, "js/recovery-ui/view.js": true, "js/session-ui/view.js": true, "js/session-ui/workspace.js": true,
}

func allowed(name string) bool { return declared[name] }

// Handler has no filesystem fallback, directory listing, module aliases or SPA
// rewrite. Every module has exactly one URL; / and /index.html share HTML.
func Handler() (http.Handler, error) {
	files, err := verified(embedded)
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if r.URL.Path == "/" {
			name = "index.html"
		}
		b, ok := files[name]
		if !ok || r.URL.Path != "/" && r.URL.Path != "/"+name {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; connect-src 'self'; style-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; object-src 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		contentType := mime.TypeByExtension(path.Ext(name))
		if path.Ext(name) == ".js" {
			contentType = "text/javascript; charset=utf-8"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", fmt.Sprint(len(b)))
		w.WriteHeader(http.StatusOK)
		if r.Method == "GET" {
			_, _ = w.Write(b)
		}
	}), nil
}

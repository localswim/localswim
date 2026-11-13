package v2

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/localswim/localswim/web"
)

const browserCSP = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; connect-src 'self'; font-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

func (x *request) serveBrowser() bool {
	if strings.Contains(x.r.URL.EscapedPath(), "%") {
		return false
	}
	var file, media string
	switch x.r.URL.Path {
	case "/":
		file, media = "index.html", "text/html; charset=utf-8"
	case "/assets/app.mjs":
		file, media = "app.mjs", "text/javascript; charset=utf-8"
	case "/assets/app.css":
		file, media = "app.css", "text/css; charset=utf-8"
	case "/assets/board.css":
		file, media = "board.css", "text/css; charset=utf-8"
	case "/assets/inter-variable.woff2":
		file, media = "inter-variable.woff2", "font/woff2"
	case "/assets/OFL.txt":
		file, media = "OFL.txt", "text/plain; charset=utf-8"
	default:
		if key, ok := strings.CutPrefix(x.r.URL.Path, "/board/"); ok {
			if !browserBoardKey(key) {
				return false
			}
		} else {
			id, ok := strings.CutPrefix(x.r.URL.Path, "/boards/")
			if _, valid := normalizeID(id); !ok || !valid {
				return false
			}
		}
		file, media = "index.html", "text/html; charset=utf-8"
	}
	if x.r.Method != http.MethodGet && x.r.Method != http.MethodHead {
		x.w.Header().Set("Allow", "GET, HEAD")
		x.problem(http.StatusMethodNotAllowed, "method_not_allowed", "This method is not available for the resource.", "", nil)
		return true
	}
	data, err := web.Assets.ReadFile("generated/" + file)
	if err != nil {
		x.fail(err)
		return true
	}
	h := x.w.Header()
	h.Set("Content-Security-Policy", browserCSP)
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	h.Set("Content-Type", media)
	h.Set("Content-Length", strconv.Itoa(len(data)))
	x.w.WriteHeader(http.StatusOK)
	if x.r.Method == http.MethodGet {
		_, _ = x.w.Write(data)
	}
	return true
}

func browserBoardKey(key string) bool {
	if len(key) == 0 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

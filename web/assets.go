package web

import "embed"

// Assets contains the optimized browser release and its matching source maps.
//
//go:embed dist/index.html dist/app.js dist/app.js.map dist/app.css dist/app.css.map
var Assets embed.FS

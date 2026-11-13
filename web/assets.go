package web

import "embed"

// Assets contains the optimized clean-room browser release.
//
//go:embed generated/index.html generated/app.mjs generated/app.css generated/board.css generated/inter-variable.woff2 generated/OFL.txt
var Assets embed.FS

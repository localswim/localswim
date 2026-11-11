package web

import "embed"

// Assets contains the production browser entry point and its local dependencies.
//
//go:embed index.html app.mjs api.mjs operation-id.mjs app.css
var Assets embed.FS

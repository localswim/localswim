package web

import "embed"

// Assets contains the production browser entry point and its local dependencies.
//
//go:embed index.html api.mjs board-updates.mjs operation-id.mjs card-prose.mjs app.css generated/src
var Assets embed.FS

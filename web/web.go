package web

import "embed"

// Files contains the self-contained local console.
//
//go:embed index.html app.css app.js
var Files embed.FS

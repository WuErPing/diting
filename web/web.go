package web

import "embed"

// Files contains the self-contained local console.
//
//go:embed index.html records.html app.css app.js records.js i18n.js
var Files embed.FS

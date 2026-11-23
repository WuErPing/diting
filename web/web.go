package web

import "embed"

// Files contains the self-contained local console.
//
//go:embed index.html records.html settings.html app.css app.js records.js settings.js i18n.js topics.js markdown.js voice-state.js
var Files embed.FS

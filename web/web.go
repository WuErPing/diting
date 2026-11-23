package web

import "embed"

// Files contains the self-contained local console.
//
//go:embed index.html records.html settings.html speakers.html app.css app.js records.js settings.js speakers.js i18n.js topics.js markdown.js voice-state.js voiceprints.js logo.svg favicon.svg
var Files embed.FS

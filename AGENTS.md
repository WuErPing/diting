# AGENTS.md

## Build

- Build target is `./bin/diting`: always run `go build -o bin/diting ./cmd/diting`, and run the app as `./bin/diting`. Do NOT build to the repo root (`./diting`) — that binary is not the one used to run the app, so changes appear to have no effect.
- Web assets (`web/index.html`, `web/records.html`, `web/app.css`, `web/app.js`, `web/records.js`) are embedded at compile time via `//go:embed` in `web/web.go`. After ANY frontend change you must rebuild `bin/diting` and restart it; editing the source files alone does not update a running or previously built binary.

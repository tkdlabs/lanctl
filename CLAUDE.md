# lanctl-go

Go port of lanctl — minimal LAN management tool for waking and shutting down hosts.

## Architecture

Single Go HTTP server (standard library + `chi` or similar minimal router).
Serves both REST API and `frontend/index.html`.

- Port: **8003**
- Config: `hosts.yaml` (same schema as Python lanctl)
- Frontend: `frontend/index.html` — plain HTML/JS, no build step
- Binary: Single static binary via `go build`

## Why Go

- Zero runtime dependency (no Python venv, no pip, no systemd service for Python)
- Single binary deployment
- Native concurrency for parallel host checks
- Better resource footprint

## Running

```bash
go run cmd/lanctl/main.go
# or
go build -o lanctl cmd/lanctl/main.go && ./lanctl
# then open http://localhost:8003
```

## Conventions

- Go: `gofmt`, `go mod tidy`, standard module layout
- Module: `github.com/tom/ai-dev/frontends/lanctl-go` (or whatever the real path is)

## General

- Commit after the feature/fix is completed and verified, then run `git push`.
- Work on a single bug or feature or design unless strictly necessary.
- Each commit should correspond to a single feature, bug fix, or design doc. Unless there is a good reason, a commit should mark "done" at most one bug or feature, or add a single design doc. It is acceptable to split a feature or bug into two or more sub-features/sub-bugs — in that case the original feature/bug can be marked done once all sub-items are complete.
- AFter implementing feature add unit tests or integration tests.
  - Unit tests are for edge cases and thorough testing.
  - Integration tests are for end-to-end basic scenarios eg. using API as black box.
  - More than 80% tests should be unit tests. If there are too many integration tests you should prioritize trimming them vs creating more unit tests.
- Backend test coverage should be 80% or more.
- Add new feature requests in features/ directory in separate .md file.
- When feature is done rename the feature .md file to done_(previous name).md
- When bug is detected, file a bug report to the bugs/ directory
- When bug is fixed, rename the bug .md file to done_(previous name).md in bugs/ directory
- Increment bug numbers using the existing convention in the bugs/features directories.
- Put design docs in design/ directory using .md files.


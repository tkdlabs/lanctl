# Feature 01: Project scaffolding and base HTTP server

## Description

Set up the Go module, project structure, minimal HTTP server, and frontend serving.
This is the foundation everything else builds on.

## Project structure

```
lanctl-go/
├── go.mod
├── go.sum
├── cmd/
│   └── lanctl/
│       └── main.go            # entry point
├── internal/
│   ├── config/                # YAML config loading
│   ├── network/               # WoL, port check
│   ├── ssh/                   # SSH operations
│   ├── http/                  # HTTP handlers + routes
│   └── frontend/              # static file serving
├── frontend/
│   └── index.html             # copied from Python lanctl
├── hosts.example.yaml         # copied from Python lanctl
└── features/
```

## Acceptance criteria

- [ ] `go.mod` initialized with appropriate module path
- [ ] Server listens on port 8003 (configurable via `PORT` env var)
- [ ] GET `/` serves the frontend HTML file
- [ ] Health check or placeholder GET `/api/hosts` that returns `[]`
- [ ] Graceful shutdown on SIGINT/SIGTERM

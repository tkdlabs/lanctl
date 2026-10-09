# Feature 08: Notices / Self-Diagnostics (stub)

Deferred follow-up to #12. The sync writes a machine-readable
`$DEPLOY_DIR/.config-sync/state.json`; the binary should read it and expose
`GET /api/notices` (config path, schema check, last sync result/time,
missing-key/degraded checks), with the frontend rendering a message box/banner.
Keeps the shell decoupled from the API while giving the web UI a catch for
sync and other issues. To be implemented separately.

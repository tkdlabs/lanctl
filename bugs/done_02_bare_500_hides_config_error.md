# Bug 02: a failing /api/hosts shows a bare HTTP 500

Issue: #22.

## Description

When `GET /api/hosts` fails, the dashboard reports `Error: HTTP 500` and shows an
empty host table. The server's response body carries the reason
(`{"detail": "..."}`) and `GET /api/notices` reports the same problem, but the
page discards both. A deployment that starts with no usable `hosts.yaml` —
for example a service whose `WorkingDirectory` / `$DEPLOY_DIR` is not the
directory holding `hosts.yaml` — is therefore impossible to diagnose from the
browser.

## Steps to reproduce

1. Run the server where the resolved `hosts.yaml` is missing or invalid, e.g.
   `DEPLOY_DIR` points at a directory that has no `hosts.yaml`.
2. Open the dashboard (`/`).
3. The version renders, the status bar reads `Error: HTTP 500`, and the table is
   empty. No notices banner appears.

`curl` shows what the page hid:

```
$ curl -s -o /dev/null -w '%{http_code}\n' localhost:8003/api/hosts
500
$ curl -s localhost:8003/api/hosts
{"detail":"read config /opt/lanctl/hosts.yaml: no such file or directory"}
$ curl -s localhost:8003/api/notices
{"status":"error", ... "notices":[{"level":"error","code":"config_invalid",
 "message":"Configuration is not usable: read config ..."}]}
```

## Expected behaviour

- The status bar shows the server's own message, e.g.
  `Error: read config /opt/lanctl/hosts.yaml: no such file or directory`.
- The notices banner still renders when `/api/hosts` fails, since that is where
  the config problem is explained.

## Actual behaviour

- `fetchHosts()` threw `` `HTTP ${res.status}` `` without reading the JSON body.
- `poll()` fetched hosts and notices with `Promise.all`; when the hosts request
  rejected, `renderNotices()` was skipped and the banner never appeared.

## Impact

Any config or directory problem surfaces as an opaque `HTTP 500`, sending the
operator to the server logs for information the page already had. The recent
notices feature (#19) is designed to explain exactly this, and this bug hid it.

## Fix

- Add `fetchJSON()`, which throws the server's `detail` (or the raw body) for a
  non-2xx response instead of `HTTP <status>`.
- Fetch hosts and notices with `Promise.allSettled` so the notices banner
  renders even when `/api/hosts` fails.
- Guard both behaviours with `TestIndexHTML_SurfacesHostsErrors`
  (`internal/frontend/server_test.go`).

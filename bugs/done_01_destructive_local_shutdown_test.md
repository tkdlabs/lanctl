# Bug 01: unit test performs a real local shutdown

## Description

`TestVMShutdown_LocalVM` (`internal/api/handlers_test.go`) posts to
`POST /api/hosts/pve/vms/vm1/shutdown` for a VM declared `local: true`. The handler
(`internal/api/handlers.go`, `VMShutdown`) calls `localops.Shutdown()`, which runs
`sudo shutdown -h now` on the machine executing `go test`. There is no mocking seam,
so the test powers off whatever host runs the suite.

On hosts where passwordless `sudo` is configured for `shutdown`/`poweroff`/`systemctl`
(as this project's own deploy/sudoers setup does), the test succeeds in shutting the
machine down and the test process is killed mid-run.

## Steps to reproduce

1. On a Linux host with `NOPASSWD: /usr/bin/shutdown` (e.g. `example-host` / `example.local`),
   run `go test ./...`.
2. The machine powers off/reboots during `internal/api` tests.

## Expected behaviour

- Default `go test ./...` runs no destructive or state-changing commands.
- Local/SSH/systemd operations are injected (mockable) so handlers can be tested
  without touching the live system.
- `localops.Shutdown()` refuses to run unless explicitly enabled.

## Actual behaviour

- `TestVMShutdown_LocalVM` executes a real `sudo shutdown -h now`.
- Other tests also invoke real `sudo systemctl`, `journalctl`, and dial `127.0.0.1:22`.

## Impact

A test run can shut down the developer/CI machine. `last reboot` on the affected host
confirmed a reboot coinciding with the test run.

## Fix

- Add an injectable operations seam for localops/sshops/network used by the API handlers.
- Guard `localops.Shutdown()` behind `LANCTL_ALLOW_SHUTDOWN=1`.
- Rewrite destructive tests to use fakes; gate real-system tests behind a build tag.

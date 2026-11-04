# Bug: local user services are queried as the lanctl process user

Issue: #27.

## Symptom

On the box that runs a user service, `user_services` show as `unknown` in
`/api/hosts` (and in the UI), and log/start/stop/restart requests fail. The same
service works when another lanctl box reaches that host over SSH: status is
`active` and logs stream normally.

## Root cause

Remote user-scoped commands run over SSH as the configured `ssh_user`, with
`XDG_RUNTIME_DIR=/run/user/$(id -u)` (see `internal/sshops`). Local user-scoped
commands ignored `ssh_user` and ran `systemctl --user` / `journalctl --user` as
the **lanctl process user** (`internal/localops.runUser` used
`os.Getuid()`).

Installed as a systemd service, lanctl runs as `root`, while the units belong
to the login user (`ssh_user`). `systemctl --user` therefore talked to root's
user manager, which does not own the units, and returned `unknown` / no logs.

## Fix

Thread the host/VM `ssh_user` through the local `operations` calls and run
user-scoped commands as that user:

- `sudo -n -u <ssh_user> env XDG_RUNTIME_DIR=/run/user/<uid> systemctl --user ...`
- the same for `journalctl --user`.

Fallbacks:

- `ssh_user` unset or equal to the process user: keep the existing behavior
  (direct command with the process user's runtime dir), so dev runs and
  root-owned user services are unchanged.
- lanctl runs as a non-root user and `ssh_user` is different: return a clear
  error instead of silently looking at the wrong manager.

## Tests

- Unit tests for command construction (same user, other user as root, other
  user as non-root, lookup failure).
- Localops tests assert the user is passed to the user-scoped runner.
- API tests assert a local host and a local VM pass `ssh_user` to the local
  user-scoped operations.
- Docs/examples updated to describe the local `ssh_user` behavior.

## Status

Fixed.

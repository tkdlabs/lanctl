# Feature 11: `install.sh --dir`

Issue: #23.

## Problem

`install.sh` installs lanctl into `$DEPLOY_DIR` (default `/opt/lanctl`). Only the
environment variable can change that: the script has no flags and no `--help`,
and it silently ignores unrecognised arguments. `deploy/install-remote.sh`
already exposes `--dir DIR` (and forwards it as `DEPLOY_DIR`), so the two
installers disagree, and a mistyped directory flag is swallowed.

The failure mode is confusing: the install succeeds into the default directory,
and the service (`WorkingDirectory` / `EnvironmentFile` set to that directory)
starts without the real `hosts.yaml`. Every request then 500s with a config
error that the dashboard only recently learned to show (#22).

## Design

Add minimal flag parsing to `install.sh`, before the root check so `--help` and
argument errors work unprivileged:

- `--dir DIR` (and `--dir=DIR`) sets the install directory; it overrides
  `DEPLOY_DIR`.
- `-h` / `--help` prints usage and exits 0.
- Any other `-*` argument, or a stray positional argument, is an error.

`DEPLOY_DIR` keeps working unchanged for backwards compatibility (the remote
installer passes it), so the resolution order is:

```
--dir DIR  >  $DEPLOY_DIR  >  /opt/lanctl
```

The `--dir` value flows through the existing `@DEPLOY_DIR@` substitution in
`lanctl.service` and `lanctl-sync.service`, and into `.env`, exactly like the
environment variable does today. No other behaviour changes.

## Tests

`deploy/install_script_test.go` drives the script with bash:

- `--help` / `-h` exit 0 and document `--dir`;
- an unknown flag and a missing `--dir` value are rejected;
- `--dir NAME` parses and reaches the root gate (skipped when the tests run as
  root, where the installer would proceed and reconfigure the machine).

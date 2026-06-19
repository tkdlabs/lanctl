# Feature 04: Deployment Scripts

## Summary

Add `deploy.py` (remote/local systemd) and `start.sh` (dev) for lanctl-go.
Supports cross-compilation to any target arch, including Raspberry Pi.

## Key design decisions

- **Cross-compile locally, upload binary**: the target needs no Go toolchain, no Python,
  no venv. Just a Linux host with systemd. Especially useful for Raspberry Pi.
- **Frontend files uploaded alongside binary**: `deploy.py` copies `frontend/*.html`
  to `$DEPLOY_DIR/frontend/` so the binary finds them via `$DEPLOY_DIR`.
- **Custom systemd unit step**: the deploy_lib `backend.service.j2` template hardcodes
  Python in `ExecStart`, so we write the unit directly with `target.put_text()`.
- **Auto-detect arch**: for remote targets without `--arch`, deploy.py runs `uname -m`
  over SSH and maps it to GOARCH.

## Usage

```bash
# Local dev
./start.sh

# Deploy locally (current machine)
python deploy.py

# Deploy to server (auto-detects arch)
python deploy.py --host myserver --user tom

# Raspberry Pi 4/5 (64-bit OS)
python deploy.py --host mypi --arch arm64 --user pi

# Raspberry Pi 3/4 (32-bit Raspbian)
python deploy.py --host mypi --arch arm --user pi

# Pi without git repo (standalone)
python deploy.py --host mypi --arch arm64 --user pi --no-pull

# Upgrade code only, skip unit file reinstall
python deploy.py --host myserver --no-service-install

# Custom dirs
DEPLOY_DIR=/opt/lanctl AI_DEV_PATH=/opt/ai-dev python deploy.py --host mypi --arch arm64
```

## Produced binary sizes

| GOARCH | Binary size |
|--------|-------------|
| amd64  | ~9 MB |
| arm64  | ~9 MB |
| arm    | ~8 MB |

All binaries are statically linked — no shared library dependencies.

## Status

Done.

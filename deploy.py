#!/usr/bin/env python3
# /// script
# requires-python = ">=3.10"
# dependencies = ["jinja2>=3.1", "fabric>=3.0"]
# ///
"""Deploy lanctl-go as a systemd service.

Cross-compiles the Go binary locally and uploads it to the target alongside
the static frontend files. No Go toolchain or Python required on the target.

Usage
-----
Local deploy (native arch):

    python deploy.py

Remote deploy (auto-detects target arch via SSH uname -m):

    python deploy.py --host 192.168.1.100
    python deploy.py --host myserver --user tom --key ~/.ssh/id_ed25519

Raspberry Pi (64-bit OS):

    python deploy.py --host mypi --arch arm64 --user pi

Raspberry Pi (32-bit Raspbian):

    python deploy.py --host mypi --arch arm --user pi

Pi without the git repo (standalone, no pull):

    python deploy.py --host mypi --arch arm64 --no-pull

Override deploy dir or ai-dev path:

    DEPLOY_DIR=/opt/lanctl-go AI_DEV_PATH=/opt/ai-dev python deploy.py --host mypi --arch arm64

Override service user:

    SERVICE_USER=pi python deploy.py --host mypi --arch arm64

Upgrade code without reinstalling the systemd unit:

    python deploy.py --host myserver --no-service-install

After deploying, copy your hosts config if needed:

    scp frontends/lanctl-go/hosts.example.yaml pi@mypi:/opt/lanctl-go/hosts.yaml

Then edit it:

    ssh pi@mypi "nano /opt/lanctl-go/hosts.yaml"

Ports (for head-to-head comparison with Python lanctl):
  Python lanctl:  http://host:8003  (service: lanctl-backend.service,    dir: /opt/lanctl)
  Go lanctl:      http://host:8004  (service: lanctl-go-backend.service, dir: /opt/lanctl-go)

Environment variables (all optional):
  DEPLOY_DIR      Deploy directory on target (default: /opt/lanctl-go)
  AI_DEV_PATH     Path to ai-dev repo on target (default: /opt/ai-dev)
  SERVICE_USER    OS user the service runs as (default: --user or $USER)
"""

import argparse
import os
import platform
import shlex
import subprocess
import sys
import tempfile
from pathlib import Path

SCRIPT_DIR = Path(__file__).parent
AI_DEV_ROOT = SCRIPT_DIR.parent.parent

sys.path.insert(0, str(AI_DEV_ROOT / "workflows/deployment/src"))

from deploy_lib import (  # noqa: E402
    LocalTarget,
    SSHTarget,
    SystemdConfig,
    SystemdDeployer,
    add_no_pull_arg,
    add_no_service_install_arg,
    step_banner,
    step_create_dirs,
    step_git_pull,
    step_write_env,
)
from deploy_lib.systemd import (  # noqa: E402
    step_create_deploy_dir,
    step_enable_services,
    step_reload_daemon,
    step_restart_services,
    step_set_env_permissions,
    step_systemd_summary,
)
from deploy_lib.target import Target  # noqa: E402

BACKEND_PORT = 8004   # 8003 is taken by Python lanctl — separate port for head-to-head comparison
BINARY_NAME = "lanctl"

_ARCH_FROM_UNAME: dict[str, str] = {
    "x86_64": "amd64",
    "aarch64": "arm64",
    "armv7l": "arm",
    "armv6l": "arm",
    "i686": "386",
    "i386": "386",
}


def _detect_remote_arch(target: Target) -> str:
    """SSH to target, run `uname -m`, map to GOARCH."""
    uname = target.run("uname -m").strip()
    arch = _ARCH_FROM_UNAME.get(uname)
    if not arch:
        print(f"  WARNING: unrecognised uname -m {uname!r}, defaulting to amd64")
        return "amd64"
    print(f"  Detected: {uname!r} → GOARCH={arch}")
    return arch


def _local_arch() -> str:
    machine = platform.machine()
    return _ARCH_FROM_UNAME.get(machine, "amd64")


def _cross_compile(arch: str) -> Path:
    """Build the lanctl binary locally for linux/{arch}. Returns path to temp binary."""
    env = os.environ.copy()
    env["GOOS"] = "linux"
    env["GOARCH"] = arch
    if arch == "arm":
        env["GOARM"] = "7"

    fd, tmp_path = tempfile.mkstemp(prefix=f"lanctl-{arch}-")
    os.close(fd)
    binary = Path(tmp_path)

    print(f"  Target: linux/{arch}" + (" (GOARM=7)" if arch == "arm" else ""))
    result = subprocess.run(
        ["go", "build", "-o", str(binary), "./cmd/lanctl/"],
        cwd=str(SCRIPT_DIR),
        env=env,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        binary.unlink(missing_ok=True)
        sys.exit(f"go build failed:\n{result.stderr}")

    size_kb = binary.stat().st_size // 1024
    print(f"  OK — {size_kb} KB")
    return binary


# ── Custom deploy steps ───────────────────────────────────────────────────────

def _make_steps(arch: str) -> list:
    """Build the step list for the given target arch (captured in closures)."""

    def step_cross_compile(target: Target, config: SystemdConfig) -> None:  # noqa: ARG001
        print(f"Cross-compiling {BINARY_NAME} for linux/{arch}...")
        config._local_binary = _cross_compile(arch)  # type: ignore[attr-defined]

    def step_upload_binary(target: Target, config: SystemdConfig) -> None:
        binary: Path = config._local_binary  # type: ignore[attr-defined]
        remote = f"{config.deploy_dir}/{BINARY_NAME}"
        print(f"Uploading binary → {remote} ...")
        try:
            target.put(binary, remote)
            target.run(f"chmod 0755 {remote}")
        finally:
            binary.unlink(missing_ok=True)
        print("  Done.")

    def step_upload_frontend(target: Target, config: SystemdConfig) -> None:
        """Upload static HTML files from the local project into DEPLOY_DIR/frontend/."""
        print("Uploading frontend files...")
        local_frontend = SCRIPT_DIR / "frontend"
        remote_frontend = f"{config.deploy_dir}/frontend"
        target.makedirs(remote_frontend)
        for html_file in sorted(local_frontend.glob("*.html")):
            target.put(html_file, f"{remote_frontend}/{html_file.name}")
            print(f"  {html_file.name}")

    def step_install_go_unit(target: Target, config: SystemdConfig) -> None:
        """Write a minimal systemd unit that runs the Go binary directly."""
        if config.skip_service_install:
            print("Skipping unit installation (--no-service-install)")
            return
        print("Installing systemd unit...")
        unit = (
            "[Unit]\n"
            f"Description=lanctl — LAN host control\n"
            "After=network.target\n"
            f"StartLimitIntervalSec={config.start_limit_interval}\n"
            f"StartLimitBurst={config.start_limit_burst}\n"
            "\n"
            "[Service]\n"
            "Type=simple\n"
            f"User={config.service_user}\n"
            f"WorkingDirectory={config.deploy_dir}\n"
            f"EnvironmentFile={config.deploy_dir}/.env\n"
            f"ExecStart={config.deploy_dir}/{BINARY_NAME}\n"
            "Restart=on-failure\n"
            f"RestartSec={config.restart_delay}\n"
            "\n"
            "[Install]\n"
            "WantedBy=multi-user.target\n"
        )
        path = f"/etc/systemd/system/{config.backend_service}"
        target.run(f"printf '%s' {shlex.quote(unit)} | sudo tee {path} > /dev/null")
        print(f"  Installed {path}")

    return [
        step_banner,
        step_git_pull,
        step_create_deploy_dir,
        step_create_dirs,
        step_cross_compile,
        step_upload_binary,
        step_upload_frontend,
        step_write_env,
        step_set_env_permissions,
        step_install_go_unit,
        step_reload_daemon,
        step_enable_services,
        step_restart_services,
        step_systemd_summary,
    ]


# ── Entry point ───────────────────────────────────────────────────────────────

def main() -> None:
    deploy_dir = os.environ.get("DEPLOY_DIR", "/opt/lanctl-go")
    ai_dev_path = os.environ.get("AI_DEV_PATH", "/opt/ai-dev")
    service_user = os.environ.get("SERVICE_USER", "")

    parser = argparse.ArgumentParser(description="Deploy lanctl-go (systemd, cross-arch)")
    parser.add_argument("--host", default=None, help="SSH hostname or IP (omit for local deploy)")
    parser.add_argument("--user", default=None, help="SSH username (default: current user)")
    parser.add_argument(
        "--key",
        default=os.path.expanduser("~/.ssh/id_ed25519"),
        help="SSH private key path (default: ~/.ssh/id_ed25519)",
    )
    parser.add_argument("--port", type=int, default=22, help="SSH port (default: 22)")
    parser.add_argument(
        "--arch",
        choices=["amd64", "arm64", "arm"],
        default=None,
        help=(
            "Target GOARCH for cross-compilation. "
            "Defaults to auto-detect (remote) or native (local). "
            "Use arm64 for Raspberry Pi 4/5 on 64-bit OS, arm for 32-bit Raspbian."
        ),
    )
    add_no_pull_arg(parser)
    add_no_service_install_arg(parser)
    args = parser.parse_args()

    if not service_user:
        service_user = args.user or os.environ.get("USER", "deploy")

    # Build the target first so we can detect arch via SSH if needed.
    if args.host:
        target: Target = SSHTarget(args.host, user=args.user, key_filename=args.key, port=args.port)
    else:
        target = LocalTarget()

    # Resolve arch
    if args.arch:
        arch = args.arch
    elif args.host:
        print("Auto-detecting target architecture...")
        arch = _detect_remote_arch(target)
    else:
        arch = _local_arch()
        print(f"Local arch: {platform.machine()} → GOARCH={arch}")

    config = SystemdConfig(
        app_name="lanctl-go",   # → unit: lanctl-go-backend.service (separate from Python lanctl-backend.service)
        project_subpath="frontends/lanctl-go",
        deploy_dir=deploy_dir,
        ai_dev_path=ai_dev_path,
        backend_port=BACKEND_PORT,
        no_frontend=True,          # frontend files served by the Go binary
        service_user=service_user,
        # PORT is what main.go reads; write it alongside BACKEND_PORT for clarity
        extra_env_vars={"PORT": str(BACKEND_PORT)},
        skip_git_pull=args.no_pull,
        skip_service_install=getattr(args, "no_service_install", False),
    )

    SystemdDeployer(config, target, steps=_make_steps(arch)).deploy()

    print()
    print("Next steps:")
    if args.host:
        user_at = f"{args.user}@{args.host}" if args.user else args.host
        print(f"  Copy your hosts config to the remote:")
        print(f"    scp frontends/lanctl-go/hosts.example.yaml {user_at}:{deploy_dir}/hosts.yaml")
        print(f"  Edit it on the remote, then open: http://{args.host}:{BACKEND_PORT}")
    else:
        print(f"  sudo cp frontends/lanctl-go/hosts.example.yaml {deploy_dir}/hosts.yaml")
        print(f"  Edit {deploy_dir}/hosts.yaml, then open: http://localhost:{BACKEND_PORT}")
    print()


if __name__ == "__main__":
    main()

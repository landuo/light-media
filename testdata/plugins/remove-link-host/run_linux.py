"""Run an explicitly supplied test binary in one owned NAS scratch container.

No service changes, image pulls, production mounts, credentials, or force-removal.
Requires existing authorized SSH/Docker access. Evidence excludes host inspection
fields and the random cleanup marker. This script is not part of the product.
"""
import argparse
import hashlib
import json
from pathlib import Path
import re
import secrets
import shlex
import subprocess
import sys
import tempfile
import shutil

parser = argparse.ArgumentParser()
parser.add_argument("ssh_target", help="explicit authorized SSH user@host")
parser.add_argument("binary", type=Path)
parser.add_argument("evidence_dir", type=Path, help="new local evidence directory")
args = parser.parse_args()
if not re.fullmatch(r"[A-Za-z0-9_.-]+@[A-Za-z0-9_.-]+", args.ssh_target):
    parser.error("expected an explicit user@host; SSH options are not accepted")
binary = args.binary.resolve(strict=True)
repo = Path(__file__).resolve().parents[3]
logdir = args.evidence_dir.resolve()
logdir.mkdir(parents=True, exist_ok=False)
sha = hashlib.sha256(binary.read_bytes()).hexdigest()
name = "lm-remove-link-host-" + secrets.token_hex(6)


def ssh(command):
    return subprocess.run(
        ["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", args.ssh_target, command],
        capture_output=True, text=True, timeout=100,
    )


def checked(command):
    result = ssh(command)
    if result.returncode:
        raise RuntimeError("remote command failed: " + result.stderr[-1000:])
    return result.stdout


stage = checked("mktemp -d /tmp/light-media-remove-link-host.XXXXXXXX").strip()
if not re.fullmatch(r"/tmp/light-media-remove-link-host\.[A-Za-z0-9]{8}", stage):
    raise RuntimeError("unexpected newly created stage path")
marker = secrets.token_hex(24)
container = None
info = {"local_sha256": sha, "scope": "only cleanuphost fixture tests; no App or production media"}
try:
    checked("printf %s " + shlex.quote(marker) + " > " + shlex.quote(stage + "/owner"))
    # Provenance tests need the frozen sources/fixtures at their usual relative
    # paths. Stage only this package and its non-secret testdata, never App data.
    with tempfile.TemporaryDirectory(prefix="remove-link-host-bundle-") as temporary:
        bundle = Path(temporary) / "payload"
        host = bundle / "internal/plugins/removelink/host"
        data = bundle / "testdata/plugins/remove-link-host"
        host.mkdir(parents=True)
        data.mkdir(parents=True)
        for source in (repo / "internal/plugins/removelink/host").glob("*.go"):
            shutil.copyfile(source, host / source.name)
        for source in (repo / "testdata/plugins/remove-link-host").iterdir():
            if source.is_file():
                shutil.copyfile(source, data / source.name)
        shutil.copyfile(binary, bundle / "host.test")
        result = subprocess.run(
            ["scp", "-qr", str(bundle), args.ssh_target + ":" + stage + "/payload"],
            capture_output=True, text=True, timeout=60,
        )
    if result.returncode:
        raise RuntimeError("scp failed: " + result.stderr)
    info["remote_sha256"] = checked("sha256sum " + shlex.quote(stage + "/payload/host.test")).split()[0]
    if info["remote_sha256"] != sha:
        raise RuntimeError("binary SHA mismatch")
    checked("chmod 755 " + shlex.quote(stage + "/payload/host.test"))
    checked("docker image inspect alpine:3.22 > /dev/null")
    argv = [
        "docker", "create", "--name", name, "--network", "none", "--read-only",
        "--user", "1000:1001", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
        "--memory", "256m", "--memory-swap", "256m", "--pids-limit", "256",
        "--tmpfs", "/tmp:rw,nosuid,nodev,size=128m,mode=1777", "-e", "GOMEMLIMIT=64MiB",
        "--mount", "type=bind,src=" + stage + "/payload,dst=/work,readonly",
        "--workdir", "/work/internal/plugins/removelink/host",
        "alpine:3.22", "/work/host.test", "-test.v", "-test.count=1", "-test.timeout=70s",
    ]
    container = checked(shlex.join(argv)).strip()
    if not re.fullmatch("[a-f0-9]{64}", container):
        raise RuntimeError("invalid new container identity")
    result = ssh("docker start -a " + container)
    (logdir / "tests.log").write_text(result.stdout + result.stderr)
    info["ssh_start_exit"] = result.returncode
    after = json.loads(checked("docker inspect " + container))[0]
    info["state"] = {key: after["State"][key] for key in (
        "Status", "Running", "OOMKilled", "ExitCode", "StartedAt", "FinishedAt",
    )}
    if info["state"]["Running"]:
        raise RuntimeError("container still running; no forced cleanup permitted")
    checked("docker rm " + container)
    info["container_removed"] = True
    container = None
    info["remote_sha256_after"] = checked("sha256sum " + shlex.quote(stage + "/payload/host.test")).split()[0]
    if info["remote_sha256_after"] != sha:
        raise RuntimeError("remote binary changed")
finally:
    if container:
        try:
            state = json.loads(checked("docker inspect " + container))[0]["State"]
            info["running_on_cleanup"] = state["Running"]
            if not state["Running"]:
                checked("docker rm " + container)
                info["container_removed"] = True
                container = None
        except Exception as error:
            info["container_cleanup_error"] = str(error)
    if container is None:
        try:
            if checked("cat " + shlex.quote(stage + "/owner")).strip() != marker:
                raise RuntimeError("new stage owner mismatch")
            checked("rm -rf -- " + shlex.quote(stage))
            info["owner_marker_verified"] = True
            info["stage_removed"] = True
        except Exception as error:
            info["stage_cleanup_error"] = str(error)
    (logdir / "result.json").write_text(json.dumps(info, indent=2) + "\n")
print(json.dumps(info, indent=2))
sys.exit(0 if info.get("state", {}).get("ExitCode") == 0
         and info.get("ssh_start_exit") == 0
         and info.get("container_removed") and info.get("stage_removed") else 1)

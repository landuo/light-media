#!/usr/bin/env python3
"""Bind a local acceptance executable to its actual Go/embed inputs.

This is an external development driver, never a product runtime dependency.
Existing evidence directories are not overwritten.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time

p = argparse.ArgumentParser()
p.add_argument("--package", required=True)
p.add_argument("--output", required=True)
p.add_argument("--evidence", required=True)
p.add_argument("--os", choices=["linux", "darwin"], required=True)
p.add_argument("--arch", choices=["amd64", "arm64"], required=True)
p.add_argument("--ldflags", default="")
a = p.parse_args()
root = Path.cwd()
evidence = Path(a.evidence)
evidence.mkdir(parents=True, exist_ok=False)
output = Path(a.output).resolve()
output.parent.mkdir(parents=True, exist_ok=True)
env = os.environ | {"GOOS": a.os, "GOARCH": a.arch, "CGO_ENABLED": "0"}
command = ["go", "build", "-tags", "modernconly", "-buildvcs=false", "-trimpath"]
if a.ldflags:
    command += ["-ldflags", a.ldflags]
command += ["-o", str(output), a.package]

def digest(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()

def inputs():
    raw = subprocess.check_output(["go", "list", "-deps", "-tags", "modernconly", "-json", a.package], env=env, text=True)
    decoder, pos, paths = json.JSONDecoder(), 0, {root / "go.mod", root / "go.sum"}
    while pos < len(raw):
        while pos < len(raw) and raw[pos].isspace():
            pos += 1
        if pos == len(raw):
            break
        pkg, pos = decoder.raw_decode(raw, pos)
        for key in ["GoFiles", "CgoFiles", "SFiles", "HFiles", "SysoFiles", "EmbedFiles"]:
            paths.update(Path(pkg["Dir"]) / name for name in pkg.get(key, []))
    return {str(path): digest(path) for path in sorted(paths) if path.is_file()}

before = inputs()
start = time.monotonic()
with (evidence / "build.log").open("w") as log:
    result = subprocess.run(command, env=env, stdout=log, stderr=subprocess.STDOUT)
after = inputs()
changed = sorted(path for path in before.keys() | after.keys() if before.get(path) != after.get(path))
record = {
    "command": command, "exit_code": result.returncode,
    "environment": {key: env[key] for key in ["GOOS", "GOARCH", "CGO_ENABLED"]},
    "go_version": subprocess.check_output(["go", "version"], text=True).strip(),
    "base_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip(),
    "branch": subprocess.check_output(["git", "branch", "--show-current"], text=True).strip(),
    "dirty_status": subprocess.check_output(["git", "status", "--short"], text=True),
    "input_count": len(before), "changed_inputs": changed,
    "elapsed_seconds": round(time.monotonic() - start, 3),
    "executable": str(output),
    "sha256": digest(output) if result.returncode == 0 else None,
    "driver_sha256": digest(__file__),
}
(evidence / "inputs.json").write_text(json.dumps(before, indent=2) + "\n")
(evidence / "build.json").write_text(json.dumps(record, indent=2) + "\n")
print(json.dumps({k: record[k] for k in ["exit_code", "input_count", "changed_inputs", "elapsed_seconds", "sha256"]}))
if result.returncode or changed:
    raise SystemExit(result.returncode or 2)

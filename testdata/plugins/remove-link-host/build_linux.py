"""Build only the formal cleanuphost Linux test binary; never run App tests."""
import argparse
import os
from pathlib import Path
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument("output", type=Path, help="binary output outside testdata")
args = parser.parse_args()
repo = Path(__file__).resolve().parents[3]
output = args.output.resolve()
if output.is_relative_to(repo / "testdata"):
    parser.error("test binaries must remain outside testdata")
output.parent.mkdir(parents=True, exist_ok=True)
env = dict(os.environ, GOOS="linux", GOARCH="amd64", CGO_ENABLED="0")
subprocess.run(
    ["go", "test", "-c", "-o", str(output), "./internal/plugins/removelink/host"],
    cwd=repo, env=env, check=True,
)

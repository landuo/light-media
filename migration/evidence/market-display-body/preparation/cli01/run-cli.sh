#!/usr/bin/env bash
set -euo pipefail
[[ "$#" == 0 ]]
export MARKET_DISPLAY_BODY_CLI_GRANTED=1
exec python3 /workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/target-drafts/market-display-body01/cli01/normal-cli-driver.py --server /workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/runtime/market-display-body-server01 --output /workspace/scratch/a1d82674c6f8/plugin-platform-recovery-20261031T1305/test-instances/market-display-body-cli01 --expected-server-sha 609ecad85dbd818473d15a0d939a9874ca2b4320612c920fe59617efbfffb05c --expected-driver-sha a3b962283da16f975def4000371a5ef5006534627ae11cbcf314d2ecc69f3606 --expected-bindings-sha 4d194a0044eae3e809c9fc060c6f07dc65b8b7ba0d4beb1c84dcb59efab2e8f0

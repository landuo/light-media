#!/bin/sh
# Native artifacts paired with the read-only NAS cloakbrowser 0.5.6 metadata.
# The fixed MoviePilot requirements.in constraint is cloakbrowser~=0.5.3.
set -eu
destination=${1:-/usr/local/lib/light-media/cloakbrowser}
architecture=${2:-$(uname -m)}
mkdir -p "$destination"
case "$architecture" in
    amd64|x86_64)
        platform=linux-x64
        version=146.0.7680.177.5
        checksum=4a12bcde95fa1bb1beef2b41ab5e5c27c36be78e3be3d0dac8c64d705216670e
        ;;
    arm64|aarch64)
        platform=linux-arm64
        version=146.0.7680.177.3
        checksum=8b71ce53b4fd131327331a31fba3835d71882d19bfaabde78dd0f5390bd16f45
        ;;
    *)
        # The original wrapper has no native binary for this CPU either. Keep
        # the rest of the application buildable; the resource returns None and
        # a concrete browser_executable_unavailable diagnostic when requested.
        printf '{"browser_runtime":"unavailable","architecture":"%s"}\n' "$architecture"
        exit 0
        ;;
esac
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT HUP INT TERM
curl --fail --location --silent --show-error --retry 2 \
    "https://cloakbrowser.dev/chromium-v$version/cloakbrowser-$platform.tar.gz" \
    --output "$temporary/browser.tar.gz"
printf '%s  %s\n' "$checksum" "$temporary/browser.tar.gz" | sha256sum --check --status
tar --extract --gzip --file "$temporary/browser.tar.gz" --directory "$destination" --no-same-owner
printf '{"browser_runtime":"installed","platform":"%s","version":"%s","sha256":"%s"}\n' "$platform" "$version" "$checksum"

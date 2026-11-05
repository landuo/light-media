CloakBrowser native runtime is downloaded from the publisher's fixed releases,
independently of any old MoviePilot installation. No Python or Node driver is
used by light-media.

The production `Dockerfile` installs this runtime in a Debian Bookworm glibc
image, together with Chromium's ordinary shared libraries, fonts and Xvfb for
the source helper's default headed mode. The Go server controls the native
browser through CDP and removes each temporary profile/display after use.
Browser downloads happen during the image build; plugin calls do not install
or update browser binaries. Frontend Node.js remains confined to its build stage.

The fixed MoviePilot commit requires `cloakbrowser~=0.5.3`. Read-only metadata
from the NAS installation identifies wrapper 0.5.6 and Playwright 1.62.0. The
0.5.6 wheel's Linux defaults are Chromium 146.0.7680.177.5 for amd64 and
146.0.7680.177.3 for arm64. Publisher SHA256SUMS signatures and version binding
were verified with the Ed25519 public key from that wheel. Download checksums
are fixed in `scripts/install-browser-runtime.sh`; background native updates
are deliberately replaced with reproducible image updates.

`internal/browserrender/flags.go` adapts ordinary launch flags from
Microsoft Playwright 1.62.0 `chromiumSwitches.ts`, licensed under Apache 2.0.
CloakBrowser's wrapper configuration is MIT licensed. The corresponding
license texts are preserved here. Chromium includes third-party notices in
its native runtime and `chrome://credits`.

Sources:

- https://pypi.org/project/cloakbrowser/0.5.6/
- https://github.com/CloakHQ/CloakBrowser
- https://github.com/microsoft/playwright/blob/v1.62.0/packages/playwright-core/src/server/chromium/chromiumSwitches.ts

Signed manifests, wheel hashes, source/target mappings and actual execution
evidence are in
`testdata/plugin-host-services-20261031/shared-runtime-review/`.

Real DOM rendering does not prove success against any public anti-bot or
Cloudflare challenge. Such public-service tests are not claimed.

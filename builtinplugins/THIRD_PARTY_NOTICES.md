# Third-party notices

## Source credits and inherited versions

The worker manifest identifies landuo as the maintainer of this Light Media distribution. The credits below record the names and versions carried over from the original plugins. Each plugin directory documents its source and license.

| Plugin | Inherited version | Source credits |
| --- | --- | --- |
| AutoBackup | 2.1.4 | [thsrite](https://github.com/thsrite) |
| AutoSignIn | 2.9.15 | [thsrite](https://github.com/thsrite) |
| BrushFlowLanduo | 5.2.18 | [jxxghp](https://github.com/jxxghp), [landuo](https://github.com/landuo) |
| CategoryEditor | 1.3 | [jxxghp](https://github.com/jxxghp) |
| CleanInvalidSeed | 2.1 | [DzAvril](https://github.com/DzAvril) |
| CloudflareSpeedTest | 1.5.1 | [thsrite](https://github.com/thsrite) |
| CustomHosts | 1.2.2 | [thsrite](https://github.com/thsrite) |
| IYUUAutoSeed | 2.22 | jxxghp, CKun |
| LarkMessager | 0.9.1 | [ui-beam-9](https://github.com/ui-beam-9) |
| MediaServerRefresh | 1.3.3 | [jxxghp](https://github.com/jxxghp) |
| MergeMessageNotify | 1.20.3 | [hotlcc](https://github.com/hotlcc) |
| MergeSiteSwitch | 1.2 | [hotlcc](https://github.com/hotlcc) |
| PluginUnInstall | 2.2 | [thsrite](https://github.com/thsrite) |
| RemoteIdentifiers | 2.4.1 | [honue](https://github.com/honue) |
| RemoveLink | 2.18 | [DzAvril](https://github.com/DzAvril) |
| ScrapeFileClean | 1.0.7 | [xlmc](https://github.com/xlmc) |
| SeedRescue | 1.6.3 | [landuo](https://github.com/landuo) |
| SiteStatistic | 1.9.7 | [lightolly](https://github.com/lightolly), [jxxghp](https://github.com/jxxghp) |
| SubscribeGroup | 2.8.7 | [thsrite](https://github.com/thsrite) |
| SystemProcess | 1.1 | [hotlcc](https://github.com/hotlcc) |
| TorrentTransfer | 1.10.3 | [jxxghp](https://github.com/jxxghp) |

## CategoryEditor business source

CategoryEditor 1.3 was written by jxxghp for MoviePilot. Its fixed source is
`plugins/categoryeditor/__init__.py` in
`https://github.com/jxxghp/MoviePilot-Plugins`, commit
`ce3357c0f2bb4e46f5ff9b72560505a33b26a362`.
The installed source SHA-256 is
`24a8109b5c15bb73b845e42653933d09eb83b608a53303fbab9ebff39b801c27`.

This repository contains a Go adaptation for the Light Media standard worker
protocol, not the original Python runtime. `LICENSE.upstream` preserves the
GNU General Public License version 3 text from that fixed upstream commit.
The host performs the category file write and cache refresh within its documented
safety bounds. The package references the original icon by name.

## Go modules

The executable includes the Go runtime. Its copyright and license text are in
`third_party/Go-LICENSE`.

The shared SDK uses `golang.org/x/text v0.43.0`; its copyright and
license text are in `third_party/x-text-LICENSE`, with its additional patent
grant in `third_party/x-text-PATENTS`. The module checksums are fixed in
`go.sum`.

## SDK provenance

`SDK_ORIGIN.json` identifies the exact complete-file SDK snapshot and permitted
import relocation. The CategoryEditor adaptation and this required complete-file SDK
snapshot are distributed under GNU GPL version 3; see `LICENSE`. This grant
does not imply that the unrelated host application is public or GPL-licensed.
Go and the listed modules retain their original licenses as stated above. This repository
does not include the host application, private configuration, databases, or
private original migration logs. Controlled synthetic/source/main/browser evidence is
separately classified in migration/evidence; it contains no production
configuration or database.

## Installed plugin adaptations and MPServerStatus support

The installed TorrentTransfer1.10.3 and SiteStatistic1.9.7 business source was
written by jxxghp for MoviePilot. Their fixed installed content source, authors,
exact source SHA256, adaptations and evidence are retained in each plugin's
ORIGIN.md and migration/ledger.json. The content baseline is not represented
as a newly identified upstream commit.

The MPServerStatus1.4 Go candidate is adapted from jxxghp's installed source
SHA256 `63583e9706aeee31a181b2fefa4e80c793ae4f2b9a76c9efea5956f8b0043d90`.
Its fixed source/plugin behavior and pure codec/proxy helper reuse are described
in plugins/mpserverstatus/ORIGIN.md; this attribution does not claim acceptance.
It adds these exact runtime module dependencies:

- `github.com/andybalholm/brotli v1.2.6`: MIT text in
  third_party/brotli-LICENSE. A conservative complete-module copy of the
  unused flate subpackage's BSD notice is in third_party/brotli-flate-LICENSE.
- `golang.org/x/net v0.61.0`: BSD text in third_party/x-net-LICENSE
  and the additional Go IP grant in third_party/x-net-PATENTS. Only its
  proxy/internal-socks leaves are required here.

The pure encoding detector preserves CPython's PSF license/history, Unicode
LicenseV3 and chardet0BSD notice under plugins/mpserverstatus/encodingdetector/.
The finite requests proxy helper preserves the CPython, RequestsApache2.0
(including original NOTICE) and PySocksBSD texts under
plugins/mpserverstatus/requestsproxy/licenses/; REQUESTS-NOTICE identifies its
source and approved proxy-TLS adaptation. These tables/algorithms are Go source
and embedded assets; no Python interpreter or package is used at runtime.
The build allowlist must include all applicable notices in each corresponding
plugin ZIP and the selected release's third-party notice bundle.

## PluginUnInstall business source

PluginUnInstall 2.2 was written by thsrite for MoviePilot. The fixed installed
source SHA256 is `2210e9dea47eeccdc46208d515e45ce1222dcadf50dbfc63cbeb41f94e470487`
in the read-only installed content snapshot recorded by migration/ledger.json.
Its Go worker uses the existing GPLv3 SDK and x/text dependency, without a
Python runtime. Source algorithm, finite host adaptations and provenance are
recorded in plugins/pluginuninstall/ORIGIN.md. Author attribution is retained
in the actual worker manifest. No upstream icon is redistributed.

## AutoSignIn business source and image decoding

AutoSignIn2.9.15 identifies thsrite as its author in the fixed installed
MoviePilot source. The fixed installed
source inventory, source SHA256, runtime-free Go adaptation and original
attributions are retained in plugins/autosignin/ORIGIN.md and the single
migration ledger. The worker includes translated Pillow12.2 algorithms,
private Go/libjpeg and libwebp leaves, CPython Unicode/JSON/calendar helpers,
Requests behavior and the private MIT regular-expression engine; their
notices/licenses remain next to the implementations and in the ZIP allowlist.

Its image decoding uses golang.org/x/image v0.24.0 (BSD plus patent grant),
decoder-only private github.com/mububoki/jpeg2000 v1.0.0 (MIT) with
OpenJPEG2.5.4 arithmetic adaptation (BSD2), github.com/gen2brain/gav1d
v0.2.6 (BSD2 plus AV1 patent license), and decoder-only BSD source adapted
from github.com/hhrutter/tiff v1.0.7. The latter is a private source relocation,
not a shared module upgrade. plugins/autosignin/imagehash/ORIGIN.json and
NOTICE.txt identify exact versions, source inventories and license locations.
The TIFF behavior adapted from LibTIFF 4.7.1 retains its license in
plugins/autosignin/imagehash/tiffport/LIBTIFF-LICENSE.md and file-specific
attribution in plugins/autosignin/imagehash/NOTICE.txt.
No Python, CGo or external codec process is used by the delivered worker.

The legacy image readers retain the selected Picsetgo MIT decoder leaves and
Pillow-derived format algorithms; legacycodec/PROVENANCE.json and
PICSETGO-LICENSE.txt identify the exact selection. The AVIF RGB conversion
translates libyuv fixed-point YUV constants and byte-plane interpolation;
imagehash/LIBYUV-LICENSE, LIBYUV-PATENTS and LIBYUV-AUTHORS retain its notices.
The BLP reader derives from the CC0 Pillow plugin; source attribution is retained
with the implementation. These are Go implementations, with no WASM or native
codec dependency. Root Go minimum1.26.4 is a build prerequisite, not a runtime
interpreter requirement.

## MergeMessageNotify Markdown conversion

The Pushover and Matrix Apprise adapters use Goldmark v1.8.7, commit
`6dafb0cf955a823afd513a641b6747424c40ee5d`, under the MIT license.
The license is in `third_party/goldmark-LICENSE` and belongs in the
MergeMessageNotify package's notice inventory. The conversion reference is the
installed Python-Markdown 3.11.2. The worker uses Goldmark at runtime.

## IYUUAutoSeed business source and compatibility helpers

The installed IYUUAutoSeed 2.22 MoviePilot source identifies jxxghp and CKun
as its authors. The fixed `__init__.py` SHA-256 is
`3cde4efa9ddd36f82318bed62a6a62ab9004257d8570538f937351b65970bdb7`.
The installed source inventory and the Go adaptation's provenance are recorded
in `plugins/iyuuautoseed/ORIGIN.json`. The executable retains the authors in
its manifest and uses the GPLv3 SDK under this repository's `LICENSE`.

The detail-page parser retains the libxml2 2.14.6 MIT notice in
`plugins/iyuuautoseed/compat/htmlextract/LICENSE.libxml2`. The Go dependencies
include `golang.org/x/net` for HTML tokenization, `golang.org/x/text`, and
Brotli. Their license and patent texts are under `third_party/`. The shared
encoding detector retains its CPython, Unicode, and chardet notices under
`plugins/mpserverstatus/encodingdetector/`. The reused source value and regular
expression helpers retain their CPython, Unicode, and regexp2 notices under
`plugins/autosignin/`. The package includes the APScheduler and Requests
notices for the adapted calendar and HTTP behavior. These are Go implementations
and embedded data. The worker requires no Python runtime.

## SystemProcess business source and Linux sampler

The installed SystemProcess 1.1 MoviePilot source identifies hotlcc as its
author. The fixed `__init__.py` SHA-256 is
`d0c8c6f1e034f46ce8b7d3c1bcd0cddc0e2d6455b2a6b35daa4b8002cd39f6ac`.
`plugins/systemprocess/ORIGIN.json` records the installed source and the Go
adaptation. The actual worker manifest retains the author attribution.

The Linux worker links `github.com/shirou/gopsutil/v4 v4.26.9`,
`github.com/tklauser/go-sysconf v0.4.0`, and
`github.com/tklauser/numcpus v0.12.0`. Their license texts are retained in
`third_party/gopsutil-LICENSE`, `third_party/go-sysconf-LICENSE`, and
`third_party/numcpus-LICENSE`. The gopsutil text includes the Go Authors'
notice for its adapted binary helpers. Numcpus is by Tobias Klauser and
distributed under Apache License 2.0.

The `golang.org/x/sys v0.49.0` BSD license and patent grant are byte-identical
to those of `golang.org/x/text v0.43.0`. The package uses the shared
`third_party/x-text-LICENSE` and `third_party/x-text-PATENTS` files for both.

## AutoBackup business source

The installed AutoBackup 2.1.4 MoviePilot source identifies thsrite as its
author. The fixed `__init__.py` SHA-256 is
`dedab53b1adf96dc59879ebdd67f77fe67ea773981d4befb04ca000889f607e4`.
`plugins/autobackup/ORIGIN.json` records the original source and the scoped Go
implementation. The worker reuses the repository's native cron and JSON-value
helpers. Its manifest retains the original author attribution. No upstream icon
or Python runtime is included in the package.

## BrushFlowLanduo source and remote workbench

BrushFlowLanduo 5.2.18 retains jxxghp and landuo as the installed source
credits. `plugins/brushflowlanduo/ORIGIN.json` binds the Go migration to the
installed source and the paired host. The worker's Go regex implementation
retains its CPython and Unicode notices under `compat/pythonre`. The fourteen
reachable workbench assets are byte-identical to the installed bundle. Their
module federation, Vite, and embedded semver notices are retained under
`plugins/brushflowlanduo/frontend-licenses`. Vue and Vuetify come from the host.

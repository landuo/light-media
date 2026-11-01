# zhconv-rs 0.4.2 original dependency — narrow static admission

**STATIC READY** for the coordinator's subsequent finite source collector to load the exact recovered package and call only `zhconv(text, 'zh-hans')` on declared owned strings. This is dependency-level admission, not execution of this library, approval of an unseen collector, or blanket vendor permission.

## Bytes and provenance

Read the already acquired official PyPI JSON and download/tool receipts locally; no new request. Both archives match their official JSON URL, byte count and SHA:

- cp39-abi3 manylinux x86_64 wheel: `b8f848fe71c17cf8db8c2fd5c24adb129ae581845ad77be259863eff0adbb387`.
- sdist: `c1efa15dfecfbbb5cf0e7d2a0be04b62e2a7aeae434fc09844dd537304d3baad`.

Independently compared all four extracted wheel members and all eighteen extracted Rust/manifest/license texts against their archive member bytes. The recovered extension `c048aba5a41b4418754eeeba3457db5f5589522eab8010d646c156da66cf09e0` and package initializer `b0451ddab68bd5a5e2be6228538f409eaa28eb53adcc3bb57998ab35b0cb129b` exactly match the existing S05 oracle's historical input hashes. Its retained freeze lists `zhconv-rs==0.4.2`. The freeze's actual path is `migration/evidence/s05-query-update/source/source/python-freeze.txt` (the acquisition shorthand omitted that nested `source`). The original MoviePilot adapter `app/utils/zhconv.py` hashes to its retained `f21b834f…` binding and simply imports/calls the two-argument function. No claim that the sdist was rebuilt into this wheel is made.

## Reachable initialization and conversion

- Wheel `zhconv_rs/__init__.py` imports the sibling extension, copies its doc and optional exports. It contains no application/module discovery, requests/browser import or user-path operation.
- `static-source/pyo3/src/lib.rs:230–262`: PyO3 initializer registers functions and `ZhConverter` class. Registration of `make_converter` is not a call to it. `use std::fs::File` and `use pyo3_file` are Rust compile-time imports, not execution of file access.
- `pyo3/src/lib.rs:26–40`: positional signature is **`(text, target, wikitext=true, /)`**. Omitting the third argument takes `zhconv_mw`, not plain conversion. Explicit `None` instead unwraps to false and is not the source call. PyO3 detaches/restores the GIL around this synchronous conversion; no worker/thread launch appears in this function.
- `src/lib.rs:151–153` → `converters.rs:25–28,62–70,90–108` lazily initializes the zh-Hans converter from embedded tables/automaton. `tables.rs:46–54` uses build-time `include_str!`/`include_bytes!`; `utils.rs` decompresses that in-memory byte slice. It does not open a runtime dictionary pathname. The unsafe deserializer consumes embedded converter bytes, not the input string.
- `converter.rs:303–321,360–485` and the rule/pagerules helpers perform in-memory text matching, optional wikitext rule construction and string output. Wikitext must remain enabled, including its string markup semantics; it is not execution of HTML/JavaScript and no URL fetching occurs in this path. This review is for finite declared strings, not a resource-bound theorem for arbitrary or adversarial large rules.

## Explicitly excluded paths

`pyo3/src/lib.rs:66–105` **make_converter** accepts custom pairs, arbitrary path strings (`File::open` at81), or a Python file-like object (84–85). None of these are permitted in the collector. Also excluded: custom Python objects, other exported helpers, CLI `src/main.rs`, build.rs, Rust build/install, full MoviePilot import, requests/urllib3/browser/native probes, real network requests, environment/proxy/CA changes or unrelated file access.

## Native-binary caveat, accurately bounded

Only static `readelf -h -d -W` and `readelf -Ws -W` were run. The ELF is x86_64 DYN and exports `PyInit_zhconv_rs`; NEEDED entries are libgcc_s, libpthread, libc and the Linux loader, with no RPATH/RUNPATH. It contains native INIT/INIT_ARRAY entries and general runtime/file symbols including `getenv`, `open64`, `readlink`, `read`, `write` and pthread TLS support. They do not by themselves show those operations occur during this call, nor does their presence invalidate the statically separated file API. Conversely, this readback is **not** a syscall trace, proof of no possible native side effects, or exhaustive audit of statically linked dependencies. Official fixed bytes plus exact historical continuity and the inspected narrow source path support this scoped admission; no import, native call, ldd, install or build was performed here.

## Coordinator execution boundary

Use the fixed isolated interpreter with `-I -S -B`; add only the exact bound `static-wheel-members` package directory by the ordinary explicit collector mechanism, not a module impersonation/import hook. Freeze/check interpreter, collector, finite string inputs, source nodes/adapter, initializer and extension SHA before execution; record actual loaded module paths. Invoke only `zhconv(text, 'zh-hans')`, with third argument omitted. Retain original results/exceptions, stderr and exit without rewriting expectations. A concrete collector still needs its own fixed-input/command readback. No permission to read arbitrary paths is created by this dependency admission.

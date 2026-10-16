# Installed subscription Context control-flow reference

`../subscription-context-original-oracle.json` comes from the installed original MoviePilot source, read only on 2026-10-16. Every named source SHA is recorded and checked by `generate.py`. No application modules are imported, and no account/configuration, media or downloader state is read.

The 28 RSS cases execute the original Context-copy/rebuild AST with actual Python WordsMatcher. The 14 options cases execute the original Rust option collector. The priority reference executes the actual legacy size AST and evaluates the actual Rust default-options argument. Recording MetaInfo and media-recognition stubs deliberately verify invocation and object routing only. **This is not a full parser or RSS integration oracle.** The same-value, invalid-word and failed-combined-offset cases cannot be replaced with simple string-change tests.

Offline generator dependencies match the installed reference: `regex==2026.5.9`, `cn2an==0.5.24`. Supply a source-only extraction whose root contains `app/`; the generator rejects different source hashes.

```sh
python generate.py --source-root /absolute/path/to/extracted-source --output /absolute/path/to/oracle.json
```

This Python script is reference tooling only. It is not part of the Go application or its runtime dependencies. Detailed Chinese behavior matrices and remaining implementation boundaries are in `docs/subscription-context-original-reference.md`.

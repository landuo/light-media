# Ordinary local-storage SDK contract

This fixture covers the finite ordinary `StorageChain.exists`, `list_files`
and `delete_file` call chains exposed through the Go SDK, including module
overrides, the managed native provider, persisted instance configuration and
normal process restart. It does **not** close all of migration group S09.

The source is MoviePilot commit
`8a83efeacd3e26b82be495e17d50146e31c96a60`. The JSON oracles were recorded from
normally imported source components, not generated from the Go implementation.
`contract.json` describes the exact source construction, normalizations,
filesystem guards, observed differences and excluded probes.

## Run the Go regression

From the repository root, with Linux, Go 1.26.0, a C compiler and the existing
module dependencies available:

```sh
export GOFLAGS='-mod=readonly -buildvcs=false'
roots='^Test(SharedLocalStorage(OrdinarySourceContract|OrdinaryConsumer|ActualModuleOverrides|LargeListAndConstructorDefaults|ProductionServerCold|OrdinaryRawPathEncoding|OrdinaryCloneColdAndCapabilities|NullStorageModuleSourceContract|PathlessDeleteModuleSourceContract|OrdinaryMissingDescendant|OrdinaryLongComponent|OrdinaryMTimeSourceContract|NativeFilenameEncodingBoundary|OrdinaryMTimePrecisionSourceContract)|OriginalLocalFileItemActualFixedConstructors)$'
go test -count=1 -v -run "$roots" ./internal/app
OWNED_LOCAL_STORAGE_RACE=1 go test -race -count=1 -v -run "$roots" ./internal/app
go test -race -count=1 -v -run '^TestSharedLocalStorage' ./internal/plugins
```

These tests build and install a real Go worker, use the normal host/SDK
protocol, operate only on newly created synthetic fixtures, and exercise real
server cold starts. They do not require Python at runtime or to run the Go
regression. Do not substitute a compile-only run or accept skipped roots.

## Source replay

`replay-source.py` is a development-only source oracle runner. Its arguments
are explicit: `--source` is the fixed read-only MoviePilot checkout; `--base`
is the original evidence workspace containing the recorded dependency layout
and static installed-plugin source inputs; `--python` selects its Python 3.12
interpreter; `--out` must name a new owned output directory. It is not a
standalone dependency installer or a component of the shipped Go application.
Only this replay entry point is parameterized. The other `source_*.py` files
retain their original evidence paths and dependency layout; they are recorded
oracle drivers, not portable replay commands for an arbitrary checkout.

The narrower source drivers and their JSON observations separately cover null
storage/path dispatch, missing regular-file descendants, long components,
large timestamps, floating-point rounding and native filename encoding. The
rounding fixture preserves all original numeric values and only replaces its
synthetic root path. Keep failed earlier observations; do not overwrite them
with the repaired result.

## Deliberate limits

- Current managed roots, capability revocation, private-path and no-follow
  guards remain in force. No denied permission/identity-replacement probes
  are part of these regression commands.
- Native non-UTF-8 names and unpaired-surrogate path inputs fail closed to
  prevent aliasing a different valid filename. This is a retained safety
  boundary, not a claim of source return-value equivalence or user approval.
- Arbitrary Pydantic numeric/Unicode constructor inputs, external storage,
  transfer/monitor business, arbitrary host paths, and the paused installed
  plugins are not implemented or activated by this finite slice.
- An actual source consumer is exercised only against an owned synthetic
  fixture with unrelated business effects disabled; this does not resume its
  production plugin or schedules.

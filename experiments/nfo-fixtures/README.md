# Local synthetic NFO fixtures

Explicit M0 fixture generator, not a media organizer. It generates five XML NFO files and six 64×64 JPEG files locally. It never contacts a NAS or executes ffmpeg. Titles, actor names/roles and custom `lightmedia-m0` IDs are namespaced with the mandatory UUID; none is a real provider ID.

Run from repository root. A lowercase canonical UUID is required:

```sh
go run ./experiments/nfo-fixtures --experiment-id 00000000-0000-4000-8000-000000000001
```

This is dry-run. Add `--apply` to create the default `work/nfo-live-fixture-v2` tree, or select a new `--out-dir work/another-new-directory`. The parent must already exist under local work and contain no symlink alias. Existing roots are refused. On partial failure, inspect the newly created directory; the generator never silently overwrites or deletes it.

This reproduces the historical M0 sample layout: Movie (2024), Series S01E02, Animation S02E01. Each work's poster and fanart are intentionally identical to reproduce the actual experiment; this cannot distinguish swapped poster/background roles. Use distinct images in a separately planned future experiment to resolve that limitation.

Videos must be separately generated locally, each as a new one-second synthetic clip, with names matching the movie/episode NFO stems. For example, run the following only with a fresh path inside the generated fixture tree:

```sh
ffmpeg -n -f lavfi -i color=c=blue:s=320x240:r=24 -t 1 -an -c:v libx264 -pix_fmt yuv420p '<fresh matching NFO stem>.mkv'
```

`-n` refuses overwrite. Do not reuse real video, production hard links or real metadata-provider IDs. Copying the fixtures and preparing an isolated remote directory are separate explicitly authorized operations, not generator behavior. The image verifier currently expects the default `work/nfo-live-fixture-v2` layout.

Validation: `go test -race ./experiments/nfo-fixtures` and `go vet ./experiments/nfo-fixtures` cover dry-run, exclusive creation, XML parsing, namespaced content, historical image identity and local output scope.

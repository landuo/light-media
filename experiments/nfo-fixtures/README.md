# Local synthetic NFO fixtures

Explicit M0 fixture generator, not a media organizer. It generates five XML NFO files and six JPEG files with distinct poster/background bytes and dimensions locally. It never contacts a NAS or executes ffmpeg. Titles, actor names/roles and custom `lightmedia-m0` IDs are namespaced with the mandatory UUID; none is a real provider ID.

Run from repository root. A lowercase canonical UUID is required:

```sh
go run ./experiments/nfo-fixtures --experiment-id 00000000-0000-4000-8000-000000000001
```

This is dry-run. Add `--apply` to create the default `work/nfo-live-fixture-v2` tree, or select a new `--out-dir work/another-new-directory`. The parent must already exist under local work and contain no symlink alias. Existing roots are refused. On partial failure, inspect the newly created directory; the generator never silently overwrites or deletes it.

`--layout namespaced` is the historical default: UUID-prefixed work directories and NFO/video stems. Explicit `--layout canonical` changes only publishing paths:

```text
<exclusive UUID experiment root>/
  合成电影 (2024)/合成电影 (2024).nfo
  合成普通剧/tvshow.nfo
  合成普通剧/Season 1/合成普通剧 S01E02.nfo
  合成国漫/tvshow.nfo
  合成国漫/Season 2/合成国漫 S02E01.nfo
```

Each work directory also contains `poster.jpg` and `fanart.jpg`. Generate matching `.mkv` stems separately. The outer NAS library root must still be the exclusive `light-media-m0-test-<new UUID>` directory. XML titles, actor names/roles and custom IDs retain the UUID namespace in both layouts; all five XML documents and six image byte sequences are identical for the same UUID/image mode. The ordinary episode expectation remains season 1, episode 2. This is a controlled publishing-layout experiment, not a confirmed fix for the observed wrong episode number. Do not reuse an old live experiment UUID.

For a new local tree, add `--layout canonical --out-dir work/<fresh-directory>` to the command above; review the dry-run before `--apply`.  Default `--image-mode distinct` produces 48×72 posters and 96×54 fanart, with six different hashes across works/roles. These new fixtures have not yet been imported live. Explicit `--image-mode legacy` reproduces the historical 64×64 same-byte poster/fanart pair per work, including the recorded old hashes; this mode cannot distinguish swapped image roles. Use a new UUID and new output directory for every new experiment.

Videos must be separately generated locally, each as a new one-second synthetic clip, with names matching the movie/episode NFO stems. For example, run the following only with a fresh path inside the generated fixture tree:

```sh
ffmpeg -n -f lavfi -i color=c=blue:s=320x240:r=24 -t 1 -an -c:v libx264 -pix_fmt yuv420p '<fresh matching NFO stem>.mkv'
```

`-n` refuses overwrite. Do not reuse real video, production hard links or real metadata-provider IDs. Copying the fixtures and preparing an isolated remote directory are separate explicitly authorized operations, not generator behavior. The image verifier and offline comparator detect either complete layout, reject mixed layouts or arbitrary subdirectories, and require the same UUID metadata namespace. The image verifier accepts the selected layout under its stdin `fixture_dir` (default `work/nfo-live-fixture-v2`). Use the new output root and the same new UUID; the distinct-role verifier refuses legacy same-byte pairs.

Validation: `go test -race ./experiments/nfo-fixtures` and `go vet ./experiments/nfo-fixtures` cover dry-run, exclusive creation, XML parsing, namespaced content, historical image identity, canonical/namespaced XML and image equivalence, fixed S01E02 expectation, and local output scope.

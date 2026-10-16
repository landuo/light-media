# Installed PluginUnInstall 2.2 reference

`../uninstall-original-oracle.json` contains 31 initialization/manager cases and 5 original form cases. `source-without-metadata.json` contains actual installed methods and original file hashes; all plugin class metadata, including the private icon URL, is omitted. Eight plugin method ASTs were compared against the sanitized work copy using one local parser. Do not use the sanitized file's whole-body hash as the installed file's hash.

The generator executes the original plugin methods and real manager stop/remove/config-delete/data-delete methods. Application imports never run. Database, scheduler, event and module operations use synthetic memory adapters; file removal is confined to newly created tempfile package directories. In particular, the real manager's post-removal force=false guard makes config/data cleanup requests no-ops. No installed user configuration or one-shot value is an input.

```sh
python3 generate.py --source-snapshot source-without-metadata.json --output /absolute/path/oracle.json --form-output /absolute/path/form.json
```

This Python reference is offline test tooling, not a Go application runtime dependency. Normal, falsey/null, missing flags, independent flags, installed-order and duplicate behavior, unknown targets, Python membership containers, self-uninstall, ignored package errors and save failures are retained. The form reference preserves stable plugin_order sorting, missing-vs-null attribute fallback and original component props.

The source methods only inspect their synthetic ROOT_PATH/app/plugins code package. This is distinct from plugin data files and must not be broadened to arbitrary paths by a host implementation.

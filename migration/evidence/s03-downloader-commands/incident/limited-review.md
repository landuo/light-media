# Bounded static incident review

`INCIDENT.json` retains every line containing a backtick or dollar sign from the two known affected final, commit-verified qB source bodies, plus sequential raw-text backtick pairs and line numbers. This is a static extraction, not shell parsing or a replay.

* `torrents.py`: 584 backticks across 173 marked lines; no dollar-sign lines. Examples: line 72, `~TorrentsAPIMixIn.torrents_properties`; line 74, the angle-bracketed qB API documentation URL; line 91, `TorrentFilesList`.
* `request.py`: 154 backticks across 34 marked lines; no dollar-sign lines. Examples: line 178, `~urllib.parse.ParseResult`; lines 440/442/443, Sphinx references to `APINames`, `_request`, and `requests.Response`.

These source docstrings are inert when treated as data, but unsafe in the original outer shell double quotes. Backticks are command-substitution syntax there; documentation references and angle brackets can also be interpreted as command words/redirections. The lack of dollar signs does not remove the backtick risk. Consecutive backticks and unknown chunk boundaries mean the retained raw pairs are **not asserted to be actual executed commands**. A source segment being a Sphinx link does not establish harmlessness.

The only observed draft-content failures retained are the two byte-count/hash mismatches recorded in `ACQUISITION-WRITE-INCIDENT.md`. Original chunk/time/stderr/exit are unavailable. Other shell side effects remain **unknown**; this audit cannot clear them by inspecting final content or by guessing that commands failed. No session-log access, source execution, download, network probe, broad scan, or cleanup was used for this review.

Current full vendor bodies and exact-tag acquisition metadata remain preserved under this audit and are content-verifiable. They may support a static native contract, but their correct final hashes do not convert the acquisition incident into a no-side-effect proof. The coordinator retains the execution hold / disposition decision.

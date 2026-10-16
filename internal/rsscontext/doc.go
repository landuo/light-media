// Package rsscontext ports the installed MoviePilot cached RSS Context producer,
// preflight and subscription identity prefix, plus isolated plugin candidates.
// It uses complete releasemeta.MetaObject state, never a five-field projection.
// IdentityCandidate does not authorize downloading: library lifecycle, rules,
// best-version handling and download transactions remain explicit host stages.
// MoviePilot GPL-3.0 source hashes and offline reference provenance are recorded
// in SOURCE.json and testdata/plugins/rss-context-full. No Python runtime is used.
package rsscontext

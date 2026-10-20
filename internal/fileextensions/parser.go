package fileextensions

// ParserMediaExtensions is the original MetaInfo media_exts parameter, in source
// order, including subtitles/audio. It is distinct from TransferChain's primary
// media predicate and constructor allowed-list order. The caller supplies the
// observed configuration frame; this function does not read a live environment.
func ParserMediaExtensions(snapshot Snapshot) ([]string, error) {
	if !snapshot.Valid() {
		return nil, ErrDependency
	}
	out := make([]string, 0)
	for _, key := range []string{MediaKey, SubtitleKey, AudioKey} {
		out = append(out, snapshot.Values(key)...)
	}
	return out, nil
}

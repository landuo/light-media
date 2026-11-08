package download

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	"light-media/internal/metainfo"
)

func addHashes(in AddRequest, kind string) ([]string, error) {
	if in.Magnet == "" {
		return torrentHashes(in.Torrent, kind)
	}
	if len(in.Torrent) != 0 {
		return nil, failure("add", "ambiguous_material")
	}
	hash, err := metainfo.MagnetHash(in.Magnet)
	if err != nil {
		return nil, failure("add", "invalid_magnet")
	}
	if len(hash) == 64 {
		if kind == "transmission" {
			return nil, failure("add", "pure_v2_unsupported_by_transmission")
		}
		// This matches the existing qB/libtorrent v2 TorrentID projection.
		hash = hash[:40]
	}
	// A hybrid magnet's two exact topics are not authenticated by info bytes.
	// Lookup and acceptance use only its canonical btih, not its second topic.
	return []string{hash}, nil
}

// torrentHashes hashes the original info dictionary, never a re-encoded map.
// Hybrid qB IDs depend on its libtorrent build, so both known IDs are checked.
func torrentHashes(data []byte, kind string) ([]string, error) {
	summary, err := metainfo.ParseMaterial(data)
	if err != nil {
		return nil, failure("add", "invalid_metainfo")
	}
	if !summary.V1 && kind == "transmission" {
		return nil, failure("add", "pure_v2_unsupported_by_transmission")
	}
	pos := 1
	for pos < len(data)-1 {
		key, start, ok := stringEnd(data, pos)
		if !ok {
			return nil, failure("add", "invalid_metainfo")
		}
		end, ok := valueEnd(data, start, 0)
		if !ok {
			return nil, failure("add", "invalid_metainfo")
		}
		if string(key) == "info" {
			hashes := []string{}
			if summary.V1 {
				h := sha1.Sum(data[start:end])
				hashes = append(hashes, hex.EncodeToString(h[:]))
			}
			if summary.V2 && kind == "qbittorrent" {
				// qB/libtorrent 2 TorrentID prefers the first 20 SHA-256 bytes.
				h := sha256.Sum256(data[start:end])
				hashes = append(hashes, hex.EncodeToString(h[:20]))
			}
			return hashes, nil
		}
		pos = end
	}
	return nil, failure("add", "missing_info")
}
func stringEnd(data []byte, pos int) ([]byte, int, bool) {
	if pos >= len(data) {
		return nil, 0, false
	}
	colon := bytes.IndexByte(data[pos:], ':')
	if colon < 1 || colon > 8 {
		return nil, 0, false
	}
	colon += pos
	n, err := strconv.Atoi(string(data[pos:colon]))
	if err != nil || n < 0 || n > len(data)-colon-1 {
		return nil, 0, false
	}
	return data[colon+1 : colon+1+n], colon + 1 + n, true
}
func valueEnd(data []byte, pos, depth int) (int, bool) {
	if depth > 64 || pos >= len(data) {
		return 0, false
	}
	switch data[pos] {
	case 'i':
		n := bytes.IndexByte(data[pos:], 'e')
		if n < 0 {
			return 0, false
		}
		return pos + n + 1, true
	case 'd', 'l':
		pos++
		for pos < len(data) && data[pos] != 'e' {
			var ok bool
			pos, ok = valueEnd(data, pos, depth+1)
			if !ok {
				return 0, false
			}
		}
		if pos >= len(data) {
			return 0, false
		}
		return pos + 1, true
	default:
		_, end, ok := stringEnd(data, pos)
		return end, ok
	}
}

// Package metainfo provides a bounded, memory-only M0 protected torrent probe.
package metainfo

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"fmt"
	"math"
	"strconv"
)

const (
	MaxBody  = 4 << 20
	MaxDepth = 64
	MaxItems = 50000
	MaxFiles = 10000
)

type Summary struct {
	InfoDictionaryValid bool  `json:"info_dictionary_valid"`
	RawInfoHashComputed bool  `json:"raw_info_hash_computed"`
	V1                  bool  `json:"v1"`
	V2                  bool  `json:"v2"`
	PieceLayersChecked  bool  `json:"piece_layers_checked"`
	FileCount           int   `json:"file_count"`
	TotalBytes          int64 `json:"total_bytes"`
	PayloadVerified     bool  `json:"payload_verified"`
}

type node struct {
	kind       byte
	text       []byte
	integer    int64
	list       []*node
	dict       []pair
	start, end int
}
type pair struct {
	key   []byte
	value *node
}

func (n *node) get(key string) *node {
	if n == nil || n.kind != 'd' {
		return nil
	}
	for _, p := range n.dict {
		if string(p.key) == key {
			return p.value
		}
	}
	return nil
}

type scanner struct {
	data       []byte
	pos, items int
}

func invalid(code string) error { return fmt.Errorf("%s", code) }

func (s *scanner) value(depth int) (*node, error) {
	if depth > MaxDepth || s.items >= MaxItems {
		return nil, invalid("bencode_resource_limit")
	}
	if s.pos >= len(s.data) {
		return nil, invalid("truncated_bencode")
	}
	s.items++
	n := &node{kind: s.data[s.pos], start: s.pos}
	switch n.kind {
	case 'i':
		s.pos++
		start := s.pos
		for s.pos < len(s.data) && s.data[s.pos] != 'e' {
			s.pos++
		}
		if s.pos == len(s.data) || s.pos-start > 20 {
			return nil, invalid("invalid_integer")
		}
		b := s.data[start:s.pos]
		if len(b) == 0 || bytes.Equal(b, []byte("-0")) || (len(b) > 1 && b[0] == '0') || (len(b) > 2 && b[0] == '-' && b[1] == '0') {
			return nil, invalid("invalid_integer")
		}
		for i, c := range b {
			if c < '0' || c > '9' {
				if i != 0 || c != '-' || len(b) == 1 {
					return nil, invalid("invalid_integer")
				}
			}
		}
		number, err := strconv.ParseInt(string(b), 10, 64)
		if err != nil {
			return nil, invalid("integer_range")
		}
		n.integer = number
		s.pos++
	case 'l', 'd':
		s.pos++
		var previous []byte
		for {
			if s.pos >= len(s.data) {
				return nil, invalid("truncated_bencode")
			}
			if s.data[s.pos] == 'e' {
				s.pos++
				break
			}
			if n.kind == 'l' {
				child, err := s.value(depth + 1)
				if err != nil {
					return nil, err
				}
				n.list = append(n.list, child)
			} else {
				key, err := s.value(depth + 1)
				if err != nil {
					return nil, err
				}
				if key.kind != 's' || len(key.text) > 4096 {
					return nil, invalid("invalid_dictionary_key")
				}
				if previous != nil && bytes.Compare(previous, key.text) >= 0 {
					return nil, invalid("duplicate_or_unsorted_key")
				}
				previous = key.text
				child, err := s.value(depth + 1)
				if err != nil {
					return nil, err
				}
				n.dict = append(n.dict, pair{key.text, child})
			}
		}
	default:
		if n.kind < '0' || n.kind > '9' {
			return nil, invalid("invalid_bencode_type")
		}
		start := s.pos
		for s.pos < len(s.data) && s.data[s.pos] >= '0' && s.data[s.pos] <= '9' {
			s.pos++
		}
		if s.pos == len(s.data) || s.data[s.pos] != ':' || s.pos-start > 8 || (s.pos-start > 1 && s.data[start] == '0') {
			return nil, invalid("invalid_string_length")
		}
		length, err := strconv.ParseUint(string(s.data[start:s.pos]), 10, 32)
		if err != nil || length > MaxBody {
			return nil, invalid("string_length_limit")
		}
		s.pos++
		if int(length) > len(s.data)-s.pos {
			return nil, invalid("truncated_string")
		}
		n.kind, n.text = 's', s.data[s.pos:s.pos+int(length)]
		s.pos += int(length)
	}
	n.end = s.pos
	return n, nil
}

func isString(n *node) bool { return n != nil && n.kind == 's' }
func positive(n *node) bool { return n != nil && n.kind == 'i' && n.integer > 0 }
func length(n *node) (int64, bool) {
	returnValue := int64(0)
	if n == nil || n.kind != 'i' || n.integer < 0 {
		return returnValue, false
	}
	return n.integer, true
}

// Inspect validates the exact bencoded info bytes and structural piece metadata.
// It cannot authenticate the tracker or verify file payload without file data.
func Inspect(data []byte) (Summary, error) {
	out := Summary{}
	if len(data) == 0 || len(data) > MaxBody {
		return out, invalid("body_limit")
	}
	s := scanner{data: data}
	root, err := s.value(0)
	if err != nil {
		return out, err
	}
	if s.pos != len(data) || root.kind != 'd' {
		return out, invalid("not_metainfo_dictionary")
	}
	info := root.get("info")
	if info == nil || info.kind != 'd' {
		return out, invalid("missing_info_dictionary")
	}
	name, piece := info.get("name"), info.get("piece length")
	if !isString(name) || len(name.text) == 0 || !positive(piece) {
		return out, invalid("invalid_info_core")
	}
	version := info.get("meta version")
	if version != nil && (version.kind != 'i' || version.integer != 2) {
		return out, invalid("unsupported_meta_version")
	}
	if version == nil || info.get("pieces") != nil {
		count, total, err := inspectV1(info, piece.integer)
		if err != nil {
			return out, err
		}
		out.V1, out.FileCount, out.TotalBytes = true, count, total
		_ = sha1.Sum(data[info.start:info.end]) // exact original bytes, never JSON output
	}
	if version != nil {
		count, total, err := inspectV2(root, info, piece.integer)
		if err != nil {
			return Summary{}, err
		}
		if out.V1 {
			legacy, modern := v1ContentLayout(info), v2ContentLayout(info.get("file tree"))
			if len(legacy) != len(modern) {
				return Summary{}, invalid("hybrid_layout_mismatch")
			}
			for path, size := range modern {
				old, ok := legacy[path]
				if !ok || old != size {
					return Summary{}, invalid("hybrid_layout_mismatch")
				}
			}
		}
		out.V2, out.PieceLayersChecked = true, true
		// A hybrid may include v1 padding files. The v2 count is content files.
		out.FileCount, out.TotalBytes = count, total
		_ = sha256.Sum256(data[info.start:info.end])
	}
	if !out.V1 && !out.V2 {
		return Summary{}, invalid("unsupported_metainfo")
	}
	out.InfoDictionaryValid, out.RawInfoHashComputed = true, true
	return out, nil
}

func inspectV1(info *node, pieceLength int64) (int, int64, error) {
	pieces := info.get("pieces")
	if !isString(pieces) || len(pieces.text)%20 != 0 {
		return 0, 0, invalid("invalid_v1_pieces")
	}
	single, files := info.get("length"), info.get("files")
	if (single == nil) == (files == nil) {
		return 0, 0, invalid("invalid_v1_layout")
	}
	count, total := 0, int64(0)
	if single != nil {
		var ok bool
		total, ok = length(single)
		if !ok {
			return 0, 0, invalid("invalid_file_length")
		}
		count = 1
	} else {
		if files.kind != 'l' || len(files.list) == 0 || len(files.list) > MaxFiles {
			return 0, 0, invalid("invalid_file_list")
		}
		seen := map[string]bool{}
		for _, file := range files.list {
			if file.kind != 'd' {
				return 0, 0, invalid("invalid_file_entry")
			}
			size, ok := length(file.get("length"))
			if !ok || size > math.MaxInt64-total {
				return 0, 0, invalid("invalid_file_length")
			}
			path := file.get("path")
			if path == nil || path.kind != 'l' || len(path.list) == 0 {
				return 0, 0, invalid("invalid_file_path")
			}
			var encoded []byte
			for _, part := range path.list {
				if !isString(part) || len(part.text) == 0 {
					return 0, 0, invalid("invalid_file_path")
				}
				encoded = strconv.AppendInt(encoded, int64(len(part.text)), 10)
				encoded = append(encoded, ':')
				encoded = append(encoded, part.text...)
			}
			if seen[string(encoded)] {
				return 0, 0, invalid("duplicate_file_path")
			}
			seen[string(encoded)] = true
			count++
			total += size
		}
	}
	expected := total / pieceLength
	if total%pieceLength != 0 {
		expected++
	}
	if expected != int64(len(pieces.text)/20) {
		return 0, 0, invalid("v1_piece_count_mismatch")
	}
	return count, total, nil
}

type v2File struct {
	size int64
	root []byte
}

func inspectV2(root, info *node, pieceLength int64) (int, int64, error) {
	if pieceLength < 16384 || pieceLength&(pieceLength-1) != 0 {
		return 0, 0, invalid("invalid_v2_piece_length")
	}
	tree, layers := info.get("file tree"), root.get("piece layers")
	if tree == nil || tree.kind != 'd' || len(tree.dict) == 0 || tree.get("") != nil || layers == nil || layers.kind != 'd' {
		return 0, 0, invalid("invalid_v2_tree_or_layers")
	}
	var files []v2File
	var walk func(*node) error
	walk = func(n *node) error {
		if n == nil || n.kind != 'd' || len(n.dict) == 0 {
			return invalid("invalid_v2_tree")
		}
		if leaf := n.get(""); leaf != nil {
			if len(n.dict) != 1 || leaf.kind != 'd' {
				return invalid("invalid_v2_leaf")
			}
			size, ok := length(leaf.get("length"))
			if !ok {
				return invalid("invalid_file_length")
			}
			r := leaf.get("pieces root")
			if size > 0 && (!isString(r) || len(r.text) != 32) {
				return invalid("invalid_v2_pieces_root")
			}
			if r != nil && (!isString(r) || len(r.text) != 32) {
				return invalid("invalid_v2_pieces_root")
			}
			if len(files) >= MaxFiles {
				return invalid("file_count_limit")
			}
			f := v2File{size: size}
			if r != nil {
				f.root = r.text
			}
			files = append(files, f)
			return nil
		}
		for _, p := range n.dict {
			if len(p.key) == 0 {
				return invalid("invalid_v2_path")
			}
			if err := walk(p.value); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(tree); err != nil {
		return 0, 0, err
	}
	total := int64(0)
	used := map[string]bool{}
	for _, f := range files {
		if f.size > math.MaxInt64-total {
			return 0, 0, invalid("total_size_overflow")
		}
		total += f.size
		if f.size <= pieceLength {
			continue
		}
		layer := layers.get(string(f.root))
		pieces := f.size / pieceLength
		if f.size%pieceLength != 0 {
			pieces++
		}
		if !isString(layer) || pieces > MaxBody/32 || int64(len(layer.text)) != pieces*32 {
			return 0, 0, invalid("missing_or_invalid_piece_layer")
		}
		if !bytes.Equal(layerRoot(layer.text, pieceLength), f.root) {
			return 0, 0, invalid("piece_layer_root_mismatch")
		}
		used[string(f.root)] = true
	}
	for _, p := range layers.dict {
		if len(p.key) != 32 || !used[string(p.key)] {
			return 0, 0, invalid("unexpected_piece_layer")
		}
	}
	return len(files), total, nil
}

func layerRoot(data []byte, pieceLength int64) []byte {
	padding := [32]byte{}
	for n := int64(16384); n < pieceLength; n *= 2 {
		both := append(append([]byte{}, padding[:]...), padding[:]...)
		padding = sha256.Sum256(both)
	}
	hashes := make([][32]byte, len(data)/32)
	for i := range hashes {
		copy(hashes[i][:], data[i*32:(i+1)*32])
	}
	for len(hashes) > 1 {
		if len(hashes)%2 != 0 {
			hashes = append(hashes, padding)
		}
		next := make([][32]byte, len(hashes)/2)
		for i := range next {
			both := append(append([]byte{}, hashes[i*2][:]...), hashes[i*2+1][:]...)
			next[i] = sha256.Sum256(both)
		}
		both := append(append([]byte{}, padding[:]...), padding[:]...)
		padding = sha256.Sum256(both)
		hashes = next
	}
	return hashes[0][:]
}

func pathKey(parts [][]byte) string {
	var key []byte
	for _, part := range parts {
		key = strconv.AppendInt(key, int64(len(part)), 10)
		key = append(key, ':')
		key = append(key, part...)
	}
	return string(key)
}

// Padding files participate in v1 piece offsets but not hybrid content layout.
func v1ContentLayout(info *node) map[string]int64 {
	out := map[string]int64{}
	if single := info.get("length"); single != nil {
		out[pathKey([][]byte{info.get("name").text})] = single.integer
		return out
	}
	for _, file := range info.get("files").list {
		if attribute := file.get("attr"); isString(attribute) && bytes.ContainsRune(attribute.text, 'p') {
			continue
		}
		var parts [][]byte
		for _, part := range file.get("path").list {
			parts = append(parts, part.text)
		}
		out[pathKey(parts)] = file.get("length").integer
	}
	return out
}
func v2ContentLayout(tree *node) map[string]int64 {
	out := map[string]int64{}
	var walk func(*node, [][]byte)
	walk = func(n *node, parts [][]byte) {
		if leaf := n.get(""); leaf != nil {
			out[pathKey(parts)] = leaf.get("length").integer
			return
		}
		for _, pair := range n.dict {
			next := append(append([][]byte{}, parts...), pair.key)
			walk(pair.value, next)
		}
	}
	walk(tree, nil)
	return out
}

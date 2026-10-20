package brushledger

import (
	"context"
	"database/sql"
	"strings"
)

// Next reads one immutable, authenticated logical value after a private path
// cursor. It owns no transaction. A caller using a DB pool can commit this read
// before yielding to code which performs network IO or starts another SQL tx.
// The cursor is not authority: scope, collection and root remain independently
// authenticated, and missing reached pages still fail closed.
func (s *Store) Next(ctx context.Context, tx *sql.Tx, c Collection, root Root, after string) (key string, value []byte, cursor string, exists bool, err error) {
	if e := s.check(tx, c, root, ""); e != nil {
		return "", nil, "", false, e
	}
	if after != "" && (len(after) != 64 || !validPath(after)) {
		return "", nil, "", false, ErrInput
	}
	if e := ctx.Err(); e != nil {
		return "", nil, "", false, e
	}
	if root == s.Empty(c) {
		return "", nil, "", false, nil
	}
	var walk func(string, string) bool
	walk = func(id, prefix string) bool {
		if len(after) >= len(prefix) && prefix < after[:len(prefix)] {
			return false
		}
		if e := ctx.Err(); e != nil {
			err = e
			return true
		}
		p, e := s.read(ctx, tx, c, id)
		if e != nil {
			err = e
			return true
		}
		if !location(p, prefix) {
			err = ErrIntegrity
			return true
		}
		if p.Kind == "leaf" {
			if !leaf(p, p.Path, p.Key) || s.keyPath(c, p.Key) != p.Path {
				err = ErrIntegrity
				return true
			}
			if p.Path <= after {
				return false
			}
			value, err = s.value(ctx, tx, c, p)
			key = p.Key
			cursor = p.Path
			exists = err == nil
			return true
		}
		if len(after) >= len(p.Path) && p.Path < after[:len(p.Path)] {
			return false
		}
		for i, child := range p.Children {
			if child != "" {
				part := string("0123456789abcdef"[i])
				if strings.HasPrefix(p.Path+part, prefix) && walk(child, p.Path+part) {
					return true
				}
			}
		}
		return false
	}
	walk(string(root), "")
	return
}

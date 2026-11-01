package app

import (
	"context"
	"io"
)

// The original six-operation source port still performs both required reads.
// No alternate filesystem API or final-read shortcut is introduced here.
type originalLocalBackupMaterialReader struct {
	ctx    context.Context
	source io.Reader
	check  func(context.Context) error
}

func (r originalLocalBackupMaterialReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if err := r.check(r.ctx); err != nil {
		return 0, err
	}
	if len(p) > 256<<10 {
		p = p[:256<<10]
	}
	n, err := r.source.Read(p)
	if denied := r.ctx.Err(); denied != nil {
		return 0, denied
	}
	if denied := r.check(r.ctx); denied != nil {
		return 0, denied
	}
	return n, err
}

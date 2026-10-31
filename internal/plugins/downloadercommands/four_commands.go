package downloadercommands

import "context"

// Optional fixed ports preserve the existing QBC/TRC contract and its old
// callers/fakes. A configured old client lacking a new port is a target wiring
// boundary, not the source's absent-client branch.
type QBTagPort interface {
	AddTags(context.Context, any, any) (any, error)
}
type QBCategoryPort interface {
	SetCategory(context.Context, any, any) (any, error)
}
type StopPort interface {
	Stop(context.Context, any) (any, error)
}

func fourCommandValue(value any) error {
	switch v := value.(type) {
	case []byte:
		return &BoundaryError{"these commands accept ordinary JSON, not bytes"}
	case []any:
		for _, item := range v {
			if err := fourCommandValue(item); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, item := range v {
			if err := valid(key); err != nil {
				return err
			}
			if err := fourCommandValue(item); err != nil {
				return err
			}
		}
	default:
		return valid(value)
	}
	return nil
}
func fourCommandReady(ctx context.Context, values ...any) error {
	if ctx == nil {
		return &BoundaryError{"missing command context"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, value := range values {
		if err := fourCommandValue(value); err != nil {
			return err
		}
	}
	return nil
}

// SetTorrentsTag returns source None, never a synthesized bool. A caught source
// exception is logged and still returns None; boundary failures stay errors.
func (q QBittorrent) SetTorrentsTag(ctx context.Context, ids, tags any) error {
	if err := fourCommandReady(ctx, ids, tags); err != nil {
		return err
	}
	if q.Client == nil {
		return nil
	}
	port, ok := q.Client.(QBTagPort)
	if !ok {
		return &BoundaryError{"configured qB client lacks the fixed add-tags port"}
	}
	_, err := port.AddTags(ctx, ids, tags)
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		return sourceFailure(ctx, q.Log, "设置种子Tag出错：", err)
	}
	return nil
}

func (q QBittorrent) SetTorrentCategory(ctx context.Context, hashString, category any) (bool, error) {
	if err := fourCommandReady(ctx, hashString, category); err != nil {
		return false, err
	}
	return effect(ctx, hashString, q.Client != nil, true, func(c context.Context, hash any) (any, error) {
		port, ok := q.Client.(QBCategoryPort)
		if !ok {
			return nil, &BoundaryError{"configured qB client lacks the fixed category port"}
		}
		if !truth(category) {
			category = ""
		}
		return port.SetCategory(c, hash, category)
	}, q.Log, "设置种子分类出错：")
}

func (q QBittorrent) StopTorrents(ctx context.Context, ids any) (bool, error) {
	if err := fourCommandReady(ctx, ids); err != nil {
		return false, err
	}
	return effect(ctx, ids, q.Client != nil, false, func(c context.Context, value any) (any, error) {
		port, ok := q.Client.(StopPort)
		if !ok {
			return nil, &BoundaryError{"configured qB client lacks the fixed stop port"}
		}
		return port.Stop(c, value)
	}, q.Log, "暂停种子出错：")
}

func (t Transmission) StopTorrents(ctx context.Context, ids any) (bool, error) {
	if err := fourCommandReady(ctx, ids); err != nil {
		return false, err
	}
	return effect(ctx, ids, t.Client != nil, false, func(c context.Context, value any) (any, error) {
		port, ok := t.Client.(StopPort)
		if !ok {
			return nil, &BoundaryError{"configured TR client lacks the fixed stop port"}
		}
		return port.Stop(c, value)
	}, t.Log, "停止种子出错：")
}

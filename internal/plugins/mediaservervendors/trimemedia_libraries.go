package mediaservervendors

import "context"

func trimeCopyString(s *string) *string {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}
func trimeCopyStrings(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string{}, s...)
}
func (p *TrimeMedia) libraryBlocked(guid string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, lib := range p.libraries {
		if lib.GUID == guid && lib.Category == TrimeOthers {
			return true
		}
	}
	if len(p.syncLibrariesRaw) > 0 {
		return ejTruth(p.syncLibrariesRaw) && !constructorMember(p.syncLibrariesRaw, "all") && !constructorMember(p.syncLibrariesRaw, guid)
	}
	if len(p.syncLibraries) == 0 {
		return false
	}
	for _, name := range p.syncLibraries {
		if name == "all" || name == guid {
			return false
		}
	}
	return true
}
func (p *TrimeMedia) ItemsCount(ctx context.Context, parent string) (*int64, error) {
	auth, err := p.IsAuthenticated(ctx)
	if err != nil || !auth {
		return nil, err
	}
	api, err := p.currentAPI(ctx, "item_count")
	if err != nil {
		return nil, err
	}
	return api.ItemCount(ctx, parent, []TrimeItemType{TrimeMovieItem, TrimeTVItem})
}
func (p *TrimeMedia) GetLibraries(ctx context.Context, hidden bool) ([]*TrimeLibrary, error) {
	auth, err := p.IsAuthenticated(ctx)
	if err != nil || !auth {
		return nil, err
	}
	p.mu.RLock()
	user := p.user
	p.mu.RUnlock()
	if user == nil {
		return nil, trimeMissing("is_admin")
	}
	api, err := p.currentAPI(ctx, "mdb_list")
	if err != nil {
		return nil, err
	}
	var rows []*TrimeMediaDB
	if user.IsAdmin == 1 {
		rows, err = api.AdminMediaDBList(ctx)
	} else {
		rows, err = api.MediaDBList(ctx)
	}
	if err != nil || rows == nil {
		return nil, err
	} // None preserves prior cache, unlike [].
	// Actual dict construction: first key insertion position, last duplicate object.
	ordered := make([]*TrimeMediaDB, 0, len(rows))
	positions := map[string]int{}
	for _, lib := range rows {
		if lib == nil {
			return nil, trimeMissing("guid")
		}
		if i, ok := positions[lib.GUID]; ok {
			ordered[i] = lib
		} else {
			positions[lib.GUID] = len(ordered)
			ordered = append(ordered, lib)
		}
	}
	p.mu.Lock()
	p.libraries = ordered
	p.assigned["_libraries"] = true
	p.mu.Unlock()
	// Source publication precedes DTO conversion. A later error retains new cache.
	out := make([]*TrimeLibrary, 0, len(ordered))
	for _, lib := range ordered {
		if hidden && p.libraryBlocked(lib.GUID) {
			continue
		}
		mediaType := "未知"
		switch lib.Category {
		case TrimeMovie:
			mediaType = "电影"
		case TrimeTV:
			mediaType = "电视剧"
		case TrimeOthers:
			continue
		}
		count, err := p.ItemsCount(ctx, lib.GUID)
		if err != nil {
			return nil, err
		}
		images := make([]string, 0, len(lib.Posters))
		for _, poster := range lib.Posters {
			api, err := p.currentAPI(ctx, "host")
			if err != nil {
				return nil, err
			}
			host, err := api.Host(ctx)
			if err != nil {
				return nil, err
			}
			images = append(images, host+poster+"?w=256")
		}
		p.mu.RLock()
		play := trimeCopyString(p.playHost)
		p.mu.RUnlock()
		linkHost := ""
		if trimeTruthy(play) {
			linkHost = *play
		} else {
			api, err := p.currentAPI(ctx, "host")
			if err != nil {
				return nil, err
			}
			linkHost, err = api.Host(ctx)
			if err != nil {
				return nil, err
			}
		}
		cookies := true
		out = append(out, &TrimeLibrary{Server: trimeString("trimemedia"), ID: trimeString(lib.GUID), Name: trimeCopyString(lib.Name), Path: trimeCopyStrings(lib.Directories), Type: trimeString(mediaType), ItemCount: count, ImageList: images, Link: trimeString(linkHost + "/library/" + lib.GUID), ServerType: trimeString("trimemedia"), UseCookies: &cookies, FieldsSet: []string{"id", "image_list", "item_count", "link", "name", "path", "server", "server_type", "type", "use_cookies"}})
	}
	return out, nil
}
func (p *TrimeMedia) RefreshRoot(ctx context.Context) (*bool, error) {
	auth, err := p.IsAuthenticated(ctx)
	if err != nil || !auth {
		return nil, err
	}
	p.mu.RLock()
	user := p.user
	p.mu.RUnlock()
	if user == nil || user.IsAdmin != 1 {
		p.log("error", "refresh-non-admin")
		result := false
		return &result, nil
	}
	api, err := p.currentAPI(ctx, "task_running")
	if err != nil {
		return nil, err
	}
	if _, err = api.TaskRunning(ctx); err != nil {
		return nil, err
	}
	p.log("info", "refresh-all")
	api, err = p.currentAPI(ctx, "mdb_scanall")
	if err != nil {
		return nil, err
	}
	return api.ScanAll(ctx)
}
func (p *TrimeMedia) RefreshItems(ctx context.Context, items []*TrimeRefreshItem) (*bool, error) {
	auth, err := p.IsAuthenticated(ctx)
	if err != nil || !auth {
		return nil, err
	}
	p.mu.RLock()
	user := p.user
	p.mu.RUnlock()
	if user == nil || user.IsAdmin != 1 {
		p.log("error", "refresh-non-admin")
		result := false
		return &result, nil
	}
	if len(items) != 0 {
		return nil, &TrimeArgumentBoundary{Field: "nonempty refresh requires original owned Path.resolve adapter"}
	}
	api, err := p.currentAPI(ctx, "task_running")
	if err != nil {
		return nil, err
	}
	if _, err = api.TaskRunning(ctx); err != nil {
		return nil, err
	}
	result := true
	return &result, nil
}

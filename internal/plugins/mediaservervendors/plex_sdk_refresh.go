package mediaservervendors

import (
	"context"
	"light-media/internal/releasemeta/legacyanime"
	"net/url"
)

func (s *PlexSDKServer) updateLibrary(ctx context.Context) (any, error) {
	library, err := s.getLibrary(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = s.queryRefresh(ctx, "/library/sections/all/refresh"); err != nil {
		return nil, err
	}
	return library, nil // original Library.update returns self
}
func (s *PlexSDKServer) refreshLibraryPath(ctx context.Context, key, parent string) error {
	_, err := s.queryRefresh(ctx, "/library/sections/"+key+"/refresh?path="+url.QueryEscape(parent))
	return err
}
func (s *PlexSDKServer) queryRefresh(ctx context.Context, key string) (*plexSDKXML, error) {
	// Only these source-owned builders call this private method; there is no
	// arbitrary endpoint/verb RPC or method parameter supplied by worker data.
	text, err := s.http.queryText(ctx, key, true)
	if err != nil {
		return nil, err
	}
	data, err := plexSDKParseXML(text)
	if err != nil {
		return nil, err
	}
	if err = s.current(ctx); err != nil {
		return nil, err
	}
	return data, nil
}
func (s *plexSDKSection) getLocations(ctx context.Context) ([]*string, error) {
	if err := s.server.current(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	cached, present := s.locations, s.locationsSet
	s.mu.RUnlock()
	if present {
		return cached, nil
	}
	locations := []*string{}
	// This is the exact constant listAttrs(...,'path',etag='Location') use: only
	// direct children, exact etag comparison, case-insensitive existence check,
	// then case-sensitive elem.attrib.get('path') (which may yield None).
	for _, element := range s.data.children {
		if element.tag != "Location" {
			continue
		}
		exists := false
		for name := range element.attributes {
			if legacyanime.LowerCase(name) == "path" {
				exists = true
				break
			}
		}
		if !exists {
			continue
		}
		if value, present := element.attributes["path"]; present {
			copy := value
			locations = append(locations, &copy)
		} else {
			locations = append(locations, nil)
		}
	}
	if err := s.server.current(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.locations, s.locationsSet = locations, true
	s.mu.Unlock()
	return locations, nil
}

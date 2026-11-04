package mediaservervendors

import (
	"context"
	"errors"
	"math"
	"math/big"
	"sync"
	"time"

	"light-media/internal/plugins/mediaserverruntime"
	"light-media/internal/releasemeta/legacyanime"
)

// PlexSDKServer is the concrete constructor/library part of the fixed PlexAPI
// object model. It owns real decoded XML and retained section objects; it does
// not derive a healthy object from configuration or substitute an empty list.
// Only the constructor/sections methods are implemented in this checkpoint.
type PlexSDKServer struct {
	http     *PlexSDKHTTP
	location *time.Location
	data     *plexSDKXML
	fields   map[string]any
	mu       sync.RWMutex
	library  *plexSDKLibrary
}
type plexSDKLibrary struct {
	server   *PlexSDKServer
	data     *plexSDKXML
	fields   map[string]any
	mu       sync.RWMutex
	sections *plexSDKSectionIndexes
}
type plexSDKSection struct {
	server       *PlexSDKServer
	mu           sync.RWMutex
	locations    []*string
	locationsSet bool
	class        string
	data         *plexSDKXML
	fields       map[string]any
}

// A separate list object preserves new-list identity even for zero members.
// Members themselves remain the cached section pointers.
type plexSDKSectionList struct{ values []*plexSDKSection }
type plexSDKSectionID struct {
	key   any
	value *plexSDKSection
}
type plexSDKSectionTitle struct {
	key    string
	values []*plexSDKSection
}
type plexSDKSectionIndexes struct {
	byID    []plexSDKSectionID
	byTitle []plexSDKSectionTitle
}

var _ PlexServer = (*PlexSDKServer)(nil)

func NewPlexSDKServer(ctx context.Context, executor *PlexSDKHTTP, location *time.Location) (*PlexSDKServer, error) {
	if executor == nil {
		return nil, errors.New("Plex SDK server requires its concrete HTTP executor")
	}
	s := &PlexSDKServer{http: executor, location: location}
	data, err := s.query(ctx, "/")
	if err != nil {
		return nil, err
	}
	fields, err := plexSDKLoadFields(data, plexSDKServerFields, location)
	if err != nil {
		return nil, err
	}
	if err = s.current(ctx); err != nil {
		return nil, err
	}
	s.data, s.fields = data, fields
	return s, nil
}
func (s *PlexSDKServer) current(ctx context.Context) error {
	if err := plexContext(ctx); err != nil {
		return err
	}
	return s.http.current(ctx)
}
func (s *PlexSDKServer) query(ctx context.Context, key string) (*plexSDKXML, error) {
	text, err := s.http.QueryText(ctx, key)
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
func (s *PlexSDKServer) Truthy(ctx context.Context) (bool, error) {
	if err := s.current(ctx); err != nil {
		return false, err
	}
	return true, nil
}
func (s *PlexSDKServer) getLibrary(ctx context.Context) (*plexSDKLibrary, error) {
	if err := s.current(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	cached := s.library
	s.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}
	data, err := s.query(ctx, "/library")
	if source, ok := err.(*mediaserverruntime.SourceError); ok && (source.Class == "BadRequest" || source.Class == "Unauthorized") {
		// Unauthorized is an original BadRequest subclass. NotFound is not.
		data, err = s.query(ctx, "/library/sections/")
	}
	if err != nil {
		return nil, err
	}
	fields := map[string]any{}
	if data != nil {
		for _, name := range []string{"identifier", "mediaTagVersion", "title1", "title2"} {
			if value, present := data.attributes[name]; present {
				fields[name] = value
			} else {
				fields[name] = nil
			}
		}
	}
	library := &plexSDKLibrary{server: s, data: data, fields: fields}
	if err = s.current(ctx); err != nil {
		return nil, err
	}
	// Python 3.12 cached_property does not serialize getters. Do not introduce
	// IO-wide locks/singleflight or cache exceptions. Each successful getter may
	// replace the cached object; this call returns the object it constructed.
	s.mu.Lock()
	s.library = library
	s.mu.Unlock()
	return library, nil
}
func (s *PlexSDKServer) Sections(ctx context.Context) (any, error) {
	library, err := s.getLibrary(ctx)
	if err != nil {
		return nil, err
	}
	indexes, err := library.getSections(ctx)
	if err != nil {
		return nil, err
	}
	if err = s.current(ctx); err != nil {
		return nil, err
	}
	// Original list(dict.values()) creates a new list, retaining member identity.
	result := make([]*plexSDKSection, 0, len(indexes.byID))
	for _, entry := range indexes.byID {
		result = append(result, entry.value)
	}
	return &plexSDKSectionList{values: result}, nil
}
func (l *plexSDKLibrary) getSections(ctx context.Context) (*plexSDKSectionIndexes, error) {
	if err := l.server.current(ctx); err != nil {
		return nil, err
	}
	l.mu.RLock()
	cached := l.sections
	l.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}
	data, err := l.server.query(ctx, "/library/sections")
	if err != nil {
		return nil, err
	}
	if data == nil {
		return nil, plexSDKSourceError("TypeError", "'NoneType' object is not iterable")
	}
	indexes := &plexSDKSectionIndexes{byID: []plexSDKSectionID{}, byTitle: []plexSDKSectionTitle{}}
	for _, element := range data.children {
		fields, err := plexSDKLoadFields(element, plexSDKSectionFields, l.server.location)
		if err != nil {
			return nil, err
		}
		class := "LibrarySection"
		switch element.attributes["type"] {
		case "movie":
			class = "MovieSection"
		case "show":
			class = "ShowSection"
		case "artist":
			class = "MusicSection"
		case "photo":
			class = "PhotoSection"
		}
		// The four ordinary subtype MROs resolve these constructor/load methods to
		// the same original owners (bound static proof). Their runtime annex remains
		// separate; this code does not reject them as a synthetic generic-only domain.
		section := &plexSDKSection{class: class, data: element, fields: fields, server: l.server}
		key := fields["key"]
		found := false
		for i := range indexes.byID {
			if plexSDKEqualKey(indexes.byID[i].key, key) {
				indexes.byID[i].value = section
				found = true
				break
			}
		}
		if !found {
			indexes.byID = append(indexes.byID, plexSDKSectionID{key: key, value: section})
		}
		title, ok := fields["title"].(string)
		if !ok {
			return nil, plexSDKSourceError("AttributeError", "'NoneType' object has no attribute 'lower'")
		}
		title = legacyanime.Strip(legacyanime.LowerCase(title))
		found = false
		for i := range indexes.byTitle {
			if indexes.byTitle[i].key == title {
				indexes.byTitle[i].values = append(indexes.byTitle[i].values, section)
				found = true
				break
			}
		}
		if !found {
			indexes.byTitle = append(indexes.byTitle, plexSDKSectionTitle{key: title, values: []*plexSDKSection{section}})
		}
	}
	if err = l.server.current(ctx); err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.sections = indexes
	l.mu.Unlock()
	return indexes, nil
}
func plexSDKEqualKey(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if x, ok := a.(*big.Int); ok {
		y, ok := b.(*big.Int)
		return ok && x.Cmp(y) == 0
	}
	// utils.cast(int, invalid) creates fresh float('nan') objects. They are
	// unequal even to another NaN, so invalid keys must not collapse together.
	if x, ok := a.(float64); ok && math.IsNaN(x) {
		return false
	}
	return false
}

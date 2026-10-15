package media

import (
	"encoding/xml"
	"fmt"
	"strconv"
)

type UniqueID struct {
	Type    string `json:"type" xml:"type,attr"`
	Default bool   `json:"default" xml:"default,attr"`
	Value   string `json:"value" xml:",chardata"`
}

type Actor struct {
	Name  string `json:"name" xml:"name"`
	Role  string `json:"role,omitempty" xml:"role,omitempty"`
	Order int    `json:"order" xml:"order"`
	Thumb string `json:"thumb,omitempty" xml:"thumb,omitempty"`
}

// NFOMetadata values must come from an explicitly confirmed identity. Episode
// UniqueIDs are episode IDs; a show's TMDB ID must not be substituted for one.
type NFOMetadata struct {
	Kind          string     `json:"kind"`
	Title         string     `json:"title"`
	OriginalTitle string     `json:"original_title,omitempty"`
	Year          int        `json:"year,omitempty"`
	Plot          string     `json:"plot,omitempty"`
	Season        int        `json:"season,omitempty"`
	Episode       int        `json:"episode,omitempty"`
	ShowTitle     string     `json:"show_title,omitempty"`
	Aired         string     `json:"aired,omitempty"`
	UniqueIDs     []UniqueID `json:"unique_ids"`
	Actors        []Actor    `json:"actors,omitempty"`
}

type nfoDocument struct {
	XMLName       xml.Name
	Title         string     `xml:"title"`
	OriginalTitle string     `xml:"originaltitle,omitempty"`
	Year          int        `xml:"year,omitempty"`
	Plot          string     `xml:"plot,omitempty"`
	Season        *int       `xml:"season,omitempty"`
	Episode       *int       `xml:"episode,omitempty"`
	ShowTitle     string     `xml:"showtitle,omitempty"`
	Aired         string     `xml:"aired,omitempty"`
	TMDBID        string     `xml:"tmdbid,omitempty"`
	UniqueIDs     []UniqueID `xml:"uniqueid"`
	Actors        []Actor    `xml:"actor,omitempty"`
}

func EncodeNFO(m NFOMetadata) ([]byte, error) {
	root := map[string]string{"movie": "movie", "tvshow": "tvshow", "episode": "episodedetails"}[m.Kind]
	if root == "" || m.Title == "" || len(m.UniqueIDs) == 0 {
		return nil, fmt.Errorf("kind, confirmed title and unique IDs are required")
	}
	if m.Year != 0 && (m.Year < 1800 || m.Year > 2199) {
		return nil, fmt.Errorf("invalid year")
	}
	seen, defaults := map[string]bool{}, 0
	tmdbID := ""
	for _, id := range m.UniqueIDs {
		if id.Type == "" || id.Value == "" || seen[id.Type] {
			return nil, fmt.Errorf("invalid or duplicate ID type")
		}
		if id.Type == "tmdb" {
			if n, err := strconv.ParseUint(id.Value, 10, 64); err != nil || n == 0 {
				return nil, fmt.Errorf("invalid TMDB ID")
			}
			tmdbID = id.Value
		}
		seen[id.Type] = true
		if id.Default {
			defaults++
		}
	}
	if defaults != 1 {
		return nil, fmt.Errorf("exactly one default unique ID is required")
	}
	d := nfoDocument{XMLName: xml.Name{Local: root}, Title: m.Title, OriginalTitle: m.OriginalTitle, Year: m.Year, Plot: m.Plot, ShowTitle: m.ShowTitle, Aired: m.Aired, UniqueIDs: m.UniqueIDs, TMDBID: tmdbID, Actors: m.Actors}
	if m.Kind == "episode" {
		if m.Season <= 0 || m.Episode <= 0 {
			return nil, fmt.Errorf("episode requires confirmed positive season and episode")
		}
		d.Season, d.Episode = &m.Season, &m.Episode
	} else if m.Season != 0 || m.Episode != 0 {
		return nil, fmt.Errorf("non-episode cannot carry episode numbering")
	}
	for _, a := range m.Actors {
		if a.Name == "" || a.Order < 0 {
			return nil, fmt.Errorf("invalid actor")
		}
	}
	body, err := xml.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(append([]byte(xml.Header), body...), '\n'), nil
}

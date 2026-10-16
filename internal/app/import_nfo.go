package app

import (
	"os"
	"path/filepath"
	"strings"

	"light-media/internal/media"
)

// NFOs are evidence offered to the user, not authority to replace the explicitly
// selected media. The normal preview still checks all episode IDs with TMDB.
func (a *App) importNFOEvidence(video string) (*importInferredMedia, *media.Numbering, string) {
	nfoName := strings.TrimSuffix(video, filepath.Ext(video)) + ".nfo"
	nfo, identifier, err := readAssetNFO(nfoName)
	if os.IsNotExist(err) {
		nfo, identifier, err = readAssetNFO(filepath.Join(filepath.Dir(video), "movie.nfo"))
	}
	if err == nil {
		switch nfo.XMLName.Local {
		case "movie":
			return &importInferredMedia{Type: "movie", MediaType: "movie", ID: identifier, Title: nfo.Title, Year: nfo.Year}, nil, ""
		case "episodedetails":
			series, seriesID, found := a.importSeriesNFO(video)
			if !found {
				return nil, nil, "episode_nfo_without_confirmed_parent_tvshow"
			}
			if nfo.ShowTitle != "" && nfo.ShowTitle != series.Title {
				return nil, nil, "episode_nfo_parent_title_conflict"
			}
			if nfo.Season == nil || nfo.Episode == nil || *nfo.Season < 0 || *nfo.Episode < 1 {
				return nil, nil, "episode_nfo_numbering_missing"
			}
			season := *nfo.Season
			return &importInferredMedia{Type: "tv", MediaType: "tv", ID: seriesID, Title: series.Title, Year: series.Year, EpisodeID: identifier}, &media.Numbering{Season: &season, Episodes: []int{*nfo.Episode}, Form: "nfo_episode"}, ""
		case "tvshow":
			return &importInferredMedia{Type: "tv", MediaType: "tv", ID: identifier, Title: nfo.Title, Year: nfo.Year}, a.parseMediaName(filepath.Base(video)).Original, ""
		default:
			return nil, nil, "unsupported_nfo_type"
		}
	}
	if !os.IsNotExist(err) {
		return nil, nil, "invalid_or_conflicting_nfo"
	}
	if series, seriesID, found := a.importSeriesNFO(video); found {
		return &importInferredMedia{Type: "tv", MediaType: "tv", ID: seriesID, Title: series.Title, Year: series.Year}, a.parseMediaName(filepath.Base(video)).Original, ""
	}
	return nil, nil, ""
}

func (a *App) importSeriesNFO(video string) (assetLocalNFO, int64, bool) {
	parent := filepath.Dir(video)
	for depth := 0; depth < 4; depth++ {
		candidate := filepath.Join(parent, "tvshow.nfo")
		if !a.importSourceAllowed(candidate, true) {
			break
		}
		nfo, identifier, err := readAssetNFO(candidate)
		if err == nil && nfo.XMLName.Local == "tvshow" {
			return nfo, identifier, true
		}
		if !os.IsNotExist(err) {
			return assetLocalNFO{}, 0, false
		}
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		parent = next
	}
	return assetLocalNFO{}, 0, false
}

package download

import (
	"context"
	"encoding/json"
	"time"

	"light-media/internal/acquisitionbatch"
)

// SourceEpisodeEffect separates the source module's original bool from an
// individual native operation's actual receipt. A failed/unknown setter is not
// silently promoted to an acknowledgment just because the source proceeds.
type SourceEpisodeEffect struct {
	Operation    string
	Acknowledged bool
	Failure      error
}
type SourceEpisodeSelection struct {
	Selection acquisitionbatch.FileSelectionResult
	Effects   []SourceEpisodeEffect
}

// SourceSelectEpisodeDownload completes the original downloader module's
// post-Add episode branch on an exact caller-owned hash. It must be called only
// after the original paused Add is acknowledged, never for a duplicate. The
// host must persist these effects with that existing source operation and must
// not repeat this workflow in response to a lost caller acknowledgment.
func (c *Client) SourceSelectEpisodeDownload(ctx context.Context, hash string, episodes []int, force bool, parse func(string) ([]int, error)) (SourceEpisodeSelection, error) {
	out := SourceEpisodeSelection{Effects: []SourceEpisodeEffect{}}
	result, e := acquisitionbatch.SelectFiles(ctx, c.config.Kind, episodes, force, acquisitionbatch.FileSelectionDependencies{
		Files: func(ctx context.Context, attempts int, interval time.Duration) ([]acquisitionbatch.EpisodeFile, error) {
			for attempt := 0; attempt < attempts; attempt++ {
				files, e := c.SourceEpisodeFiles(ctx, hash)
				out.Effects = append(out.Effects, SourceEpisodeEffect{"files", e == nil, e})
				if e == nil && len(files) > 0 {
					result := make([]acquisitionbatch.EpisodeFile, len(files))
					for i, f := range files {
						result[i] = acquisitionbatch.EpisodeFile{ID: f.ID, Name: f.Name}
					}
					return result, nil
				}
				if attempt < attempts-1 && interval > 0 {
					timer := time.NewTimer(interval)
					select {
					case <-ctx.Done():
						timer.Stop()
						// The source wrapper catches lookup exceptions and returns
						// no files; the already-accepted Add remains distinct.
						return nil, nil
					case <-timer.C:
					}
				}
			}
			return nil, nil
		},
		Episodes: parse,
		SetFiles: func(ctx context.Context, ids []json.Number, wanted bool) (bool, error) {
			ok, e := c.SourceEpisodeFileSelection(ctx, hash, ids, wanted)
			operation := "files_unwanted"
			if wanted {
				operation = "files_wanted"
			}
			out.Effects = append(out.Effects, SourceEpisodeEffect{operation, ok, e})
			return ok, nil // original setter catches exceptions, then Start runs.
		},
		Start: func(ctx context.Context, force bool) (bool, error) {
			ok, e := c.SourceStartEpisodeDownload(ctx, hash, force)
			out.Effects = append(out.Effects, SourceEpisodeEffect{"start", ok, e})
			return ok, nil // original module returns the accepted hash after False.
		},
	})
	out.Selection = result
	return out, e
}

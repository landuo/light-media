package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

const TransferHistoryBySourceCapability = "history.transfer.get_by_src"
const TransferHistoryByDestinationCapability = "history.transfer.get_by_dest"

// TransferHistoryRecord retains every source ORM column, including nullable
// values. ID is the host's stable opaque history handle, not a source SQL ID.
// FileItem and Files retain JSON null versus empty object/array distinctions.
type TransferHistoryRecord struct {
	ID                  string          `json:"id"`
	Source              *string         `json:"src"`
	SourceStorage       *string         `json:"src_storage"`
	SourceFileItem      json.RawMessage `json:"src_fileitem"`
	Destination         *string         `json:"dest"`
	DestinationStorage  *string         `json:"dest_storage"`
	DestinationFileItem json.RawMessage `json:"dest_fileitem"`
	Mode                *string         `json:"mode"`
	Type                *string         `json:"type"`
	Category            *string         `json:"category"`
	Title               *string         `json:"title"`
	Year                *string         `json:"year"`
	TMDBID              *int64          `json:"tmdbid"`
	IMDbID              *string         `json:"imdbid"`
	TVDBID              *int64          `json:"tvdbid"`
	DoubanID            *string         `json:"doubanid"`
	BangumiID           *int64          `json:"bangumiid"`
	AniListID           *int64          `json:"anilistid"`
	MediaSource         *string         `json:"media_source"`
	MediaID             *string         `json:"media_id"`
	Seasons             *string         `json:"seasons"`
	Episodes            *string         `json:"episodes"`
	Image               *string         `json:"image"`
	Downloader          *string         `json:"downloader"`
	DownloadHash        *string         `json:"download_hash"`
	Status              *bool           `json:"status"`
	Error               *string         `json:"errmsg"`
	Date                *string         `json:"date"`
	Files               json.RawMessage `json:"files"`
	EpisodeGroup        *string         `json:"episode_group"`
}

type TransferHistorySourceRequest struct {
	Source  *string `json:"src"`
	Storage *string `json:"storage,omitempty"`
}
type TransferHistoryDestinationRequest struct {
	Destination *string `json:"dest"`
	Storage     *string `json:"storage,omitempty"`
}

// Histories uses the active invocation's HostCaller. Paths are exact database
// query values, never filesystem access or an arbitrary SQL/URL channel.
type Histories struct{ Host HostCaller }

func (h Histories) TransferBySource(ctx context.Context, source *string, storage *string) (*TransferHistoryRecord, error) {
	if !validHistoryQueryStrings(source, storage) {
		return nil, errors.New("history query strings must be valid UTF-8")
	}
	return h.transfer(ctx, TransferHistoryBySourceCapability, TransferHistorySourceRequest{source, storage})
}
func (h Histories) TransferByDestination(ctx context.Context, destination *string, storage *string) (*TransferHistoryRecord, error) {
	if !validHistoryQueryStrings(destination, storage) {
		return nil, errors.New("history query strings must be valid UTF-8")
	}
	return h.transfer(ctx, TransferHistoryByDestinationCapability, TransferHistoryDestinationRequest{destination, storage})
}

// encoding/json replaces invalid UTF-8 in Go strings. Reject before encoding
// so an exact history lookup cannot silently address a different valid path.
func validHistoryQueryStrings(path, storage *string) bool {
	return (path == nil || utf8.ValidString(*path)) && (storage == nil || utf8.ValidString(*storage))
}

func (h Histories) transfer(ctx context.Context, method string, input any) (*TransferHistoryRecord, error) {
	if h.Host == nil {
		return nil, errors.New("history requires an active host caller")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	raw, err = h.Host(ctx, method, raw)
	if err != nil {
		return nil, err
	}
	var row *TransferHistoryRecord
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err = d.Decode(&row); err != nil {
		return nil, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("invalid history response")
	}
	if row != nil && row.ID == "" {
		return nil, errors.New("history response has no handle")
	}
	return row, nil
}

package seedrescue

import (
	"context"
	"encoding/json"
	"time"
)

// Every operation below belongs to the trusted host. Runtime enclosure fields
// may contain private material; CandidateRecord/JobView never persist them.
// Implementations must return owned copies and use the plugin's owner scope.
type Host interface {
	Guard(context.Context) error
	Now() time.Time
	LoadData(context.Context, string) (json.RawMessage, error)
	SaveData(context.Context, string, json.RawMessage) error
	UpdateConfig(context.Context, Object) error
	IYUUAvailable(context.Context) bool
	IYUUEnabled(context.Context) bool
	Service(context.Context, string) (*Service, error)
	Site(context.Context, int) (Object, error)
	Indexer(context.Context, string) (Object, error)
	Options(context.Context) (Object, error)
	RSS(context.Context, string, Object) ([]Object, bool, error)
	SeedInfo(context.Context, []string) (any, string, error)
	TorrentURL(context.Context, any) (string, string, error)
	Search(context.Context, string, string, int) ([]Object, error)
	Detail(context.Context, string, Object, string) (DetailResult, error)
	DownloadMetadata(context.Context, Object) ([]byte, string, error)
	QueryTorrents(context.Context, string, []string) ([]Object, error)
	DownloadingTorrents(context.Context, string, string) ([]Object, error)
	AddTorrent(context.Context, AddRequest) (bool, error)
	PostMessage(context.Context, Object) error
	Log(context.Context, string, string)
	ValidateCron(string) error
}
type Service struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Connected bool   `json:"connected"`
}
type AddRequest struct {
	Downloader string
	Content    []byte
	SavePath   *string
	Category   string
	Tags       []string
}
type DetailResult struct {
	Torrent Object  `json:"torrent"`
	Status  string  `json:"status"`
	Error   *string `json:"error"`
}
type Response struct {
	Success bool    `json:"success"`
	Message *string `json:"message"`
	Data    any     `json:"data,omitempty"`
}

func success(data any) Response    { return Response{Success: true, Data: data} }
func rejected(msg string) Response { return Response{Message: &msg} }

type Selection struct {
	CandidateID                string   `json:"candidate_id"`
	ConfirmedDownloadFactor    *float64 `json:"confirmed_download_factor"`
	ConfirmUnknown             bool     `json:"confirm_unknown"`
	SkipFutureRiskConfirmation bool     `json:"skip_future_risk_confirmation"`
}
type Schedule struct {
	ID, Name, Trigger, Cron string
	RunAt                   time.Time
	Method                  string
	Kwargs                  Object
}

package app

import (
	"encoding/json"
	"fmt"
	"light-media/internal/catalog"
	"light-media/internal/download"
	"light-media/internal/media"
	"light-media/internal/priorityrules"
	"light-media/internal/sites"
	"light-media/internal/torrentfilter"
	"time"
)

type SiteConfig struct {
	sites.Config
	SiteOptions
	Enabled     bool `json:"enabled"`
	DailyBudget int  `json:"daily_budget"`
}
type PathMapping struct {
	From string `json:"from"`
	To   string `json:"to"`
}
type DownloaderConfig struct {
	download.Config
	ClearAPIKey  bool          `json:"clear_api_key,omitempty"`
	Name         string        `json:"name"`
	DownloadPath string        `json:"download_path"`
	Mappings     []PathMapping `json:"mappings"`
	SharedPaths  bool          `json:"shared_paths,omitempty"`
	Enabled      *bool         `json:"enabled,omitempty"`
}

func downloaderEnabled(d DownloaderConfig) bool { return d.Enabled == nil || *d.Enabled }

// DirectoryRule separates downloader save paths from library destinations.
// A rule only authorizes automatic organization when explicitly enabled.
type DirectoryRule struct {
	OverwriteMode          *string `json:"overwrite_mode,omitempty"`
	OverwriteModeKnown     bool    `json:"overwrite_mode_known,omitempty"`
	Priority               int     `json:"priority"`
	MediaCategory          string  `json:"media_category,omitempty"`
	DownloadTypeFolder     bool    `json:"download_type_folder"`
	DownloadCategoryFolder bool    `json:"download_category_folder"`
	IncludeExisting        bool    `json:"include_existing"`
	ID                     string  `json:"id"`
	Name                   string  `json:"name"`
	DownloaderID           string  `json:"downloader_id"`
	DownloadPath           string  `json:"download_path"`
	SourcePath             string  `json:"source_path"`
	LibraryPath            string  `json:"library_path,omitempty"`
	RootID                 string  `json:"root_id"`
	MediaType              string  `json:"media_type"`
	Mode                   string  `json:"mode"`
	MonitorType            string  `json:"monitor_type"`
	Enabled                bool    `json:"enabled"`
	Renaming               *bool   `json:"renaming,omitempty"`
	Scraping               *bool   `json:"scraping,omitempty"`
	LibraryTypeFolder      *bool   `json:"library_type_folder,omitempty"`
}
type Root struct {
	FNOSLibraryGUID string `json:"fnos_library_guid"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	Path            string `json:"path"`
	Kind            string `json:"kind"`
}
type FNConfig struct {
	Host        string        `json:"host"`
	Username    string        `json:"username"`
	Password    string        `json:"password"`
	LibraryGUID string        `json:"library_guid"`
	Mappings    []PathMapping `json:"mappings"`
	Enabled     bool          `json:"enabled"`
}
type TMDBConfig struct {
	APIKey      string `json:"api_key"`
	AccessToken string `json:"access_token"`
}
type Settings struct {
	PluginMarkets               []string                     `json:"plugin_markets"`
	PluginLocalRepoPaths        []string                     `json:"plugin_local_repo_paths"`
	PluginAutoReload            bool                         `json:"plugin_auto_reload"`
	GitHubProxy                 string                       `json:"github_proxy,omitempty"`
	GitHubToken                 string                       `json:"github_token,omitempty"`
	RepoGitHubToken             string                       `json:"repo_github_token,omitempty"`
	PluginServerURL             *string                      `json:"plugin_server_url,omitempty"`
	PluginStatisticShare        *bool                        `json:"plugin_statistic_share,omitempty"`
	RMTMediaExt                 json.RawMessage              `json:"rmt_mediaext,omitempty"`
	RMTSubExt                   json.RawMessage              `json:"rmt_subext,omitempty"`
	RMTAudioExt                 json.RawMessage              `json:"rmt_audioext,omitempty"`
	DownloadTmpExt              json.RawMessage              `json:"download_tmpext,omitempty"`
	DefaultSub                  json.RawMessage              `json:"default_sub,omitempty"`
	DefaultMovieSubscribeConfig json.RawMessage              `json:"default_movie_subscribe_config,omitempty"`
	DefaultTVSubscribeConfig    json.RawMessage              `json:"default_tv_subscribe_config,omitempty"`
	AppDomain                   string                       `json:"app_domain,omitempty"`
	TMDBImageDomain             string                       `json:"tmdb_image_domain,omitempty"`
	RecognizePluginFirst        bool                         `json:"recognize_plugin_first"`
	SearchSource                *string                      `json:"search_source,omitempty"`
	AutoDownloadUser            string                       `json:"auto_download_user,omitempty"`
	ProxyHost                   string                       `json:"proxy_host,omitempty"`
	ClearProxyHost              bool                         `json:"clear_proxy_host,omitempty"`
	PriorityRules               *priorityrules.Configuration `json:"priority_rules,omitempty"`
	APIToken                    string                       `json:"api_token"`
	SiteDataRefreshHours        *int                         `json:"site_data_refresh_hours"`
	ReadSiteMessages            *bool                        `json:"read_site_messages"`
	IndexerSites                []string                     `json:"indexer_sites"`
	RSSSites                    []string                     `json:"rss_sites"`
	Directories                 []DirectoryRule              `json:"directories"`
	MaterialMaxMiB              int                          `json:"material_max_mib"`
	AutoImportMode              string                       `json:"auto_import_mode"`
	LogLevel                    string                       `json:"log_level"`
	LogMaxMiB                   int                          `json:"log_max_mib"`
	LogRetainFiles              int                          `json:"log_retain_files"`
	LogRetainDays               int                          `json:"log_retain_days"`
	LogTotalMiB                 int                          `json:"log_total_mib"`
	TMDB                        TMDBConfig                   `json:"tmdb"`
	Sites                       []SiteConfig                 `json:"sites"`
	Downloaders                 []DownloaderConfig           `json:"downloaders"`
	Roots                       []Root                       `json:"roots"`
	AllowedRoots                []string                     `json:"allowed_roots"`
	FNOS                        FNConfig                     `json:"fnos"`
	DefaultDownloader           string                       `json:"default_downloader"`
	DefaultRoot                 string                       `json:"default_root"`
	DebugUntil                  string                       `json:"debug_until"`
}
type Policy struct {
	FilterParams         *torrentfilter.Parameters `json:"filter_params,omitempty"`
	FilterGroups         []string                  `json:"filter_groups,omitempty"`
	Source               string                    `json:"source"`
	HDR                  string                    `json:"hdr"`
	Languages            []string                  `json:"languages"`
	Subtitles            []string                  `json:"subtitles"`
	PreferredGroups      []string                  `json:"preferred_groups"`
	PreferredResolutions []string                  `json:"preferred_resolutions"`
	MinSeeders           int                       `json:"min_seeders"`
	FreeOnly             bool                      `json:"free_only"`
	Resolution           string                    `json:"resolution"`
	Codec                string                    `json:"codec"`
	MinBytes             int64                     `json:"min_bytes"`
	MaxBytes             int64                     `json:"max_bytes"`
	Include              string                    `json:"include"`
	Exclude              string                    `json:"exclude"`
}
type Schedule struct {
	Source          string              `json:"source"`
	Rule            string              `json:"rule"`
	Weekdays        []int               `json:"weekdays"`
	EveryDays       int                 `json:"every_days"`
	StartDate       string              `json:"start_date"`
	Time            string              `json:"time"`
	Timezone        string              `json:"timezone"`
	Mode            string              `json:"mode"`
	IntervalMinutes int                 `json:"interval_minutes"`
	WaitMinutes     int                 `json:"wait_minutes"`
	WindowHours     int                 `json:"window_hours"`
	ActiveMinutes   int                 `json:"active_minutes"`
	FallbackMinutes int                 `json:"fallback_minutes"`
	Exceptions      []ScheduleException `json:"exceptions"`
}
type ScheduleException struct {
	Date       string `json:"date"`
	Action     string `json:"action"`
	TargetDate string `json:"target_date"`
	Episode    int    `json:"episode"`
}
type Subscription struct {
	SourceContextSHA   string              `json:"source_context_sha,omitempty"`
	SourceState        string              `json:"source_state,omitempty"`
	SourceNote         []int               `json:"source_note"`
	SourceSearchIMDb   bool                `json:"source_search_imdb,omitempty"`
	BestVersion        bool                `json:"best_version"`
	BestVersionFull    bool                `json:"best_version_full"`
	CurrentPriority    *int                `json:"current_priority"`
	EpisodePriority    map[string]int      `json:"episode_priority"`
	LackEpisode        *int                `json:"lack_episode"`
	ManualTotalEpisode bool                `json:"manual_total_episode"`
	Keyword            string              `json:"keyword"`
	SiteIDs            []string            `json:"site_ids"`
	TotalEpisodes      int                 `json:"total_episodes"`
	DirectoryID        string              `json:"directory_id"`
	DownloadPath       string              `json:"download_path"`
	TargetScope        string              `json:"target_scope"`
	StartEpisode       int                 `json:"start_episode"`
	ID                 string              `json:"id"`
	Media              catalog.Media       `json:"media"`
	Season             int                 `json:"season"`
	Episodes           []int               `json:"episodes"`
	Excluded           []int               `json:"excluded"`
	EpisodeDates       map[int]string      `json:"episode_dates"`
	AllowEarly         []int               `json:"allow_early"`
	Policy             Policy              `json:"policy"`
	Schedule           Schedule            `json:"schedule"`
	Mappings           []media.MappingRule `json:"mappings"`
	DownloaderID       string              `json:"downloader_id"`
	RootID             string              `json:"root_id"`
	State              string              `json:"state"`
	Revision           int                 `json:"revision"`
	LastCheck          string              `json:"last_check"`
	NextCheck          string              `json:"next_check"`
	LastReason         string              `json:"last_reason"`
	CreatedAt          string              `json:"created_at"`
}
type Intent struct {
	SourceModuleStarted     bool            `json:"source_module_started,omitempty"`
	SourceDownloader        json.RawMessage `json:"source_downloader,omitempty"`
	SeasonScopeSHA          string          `json:"season_scope_sha,omitempty"`
	SubscriptionAcquisition string          `json:"subscription_acquisition,omitempty"`
	SourcePostAddVersion    int             `json:"source_post_add_version,omitempty"`
	LarkInbox               string          `json:"lark_inbox,omitempty"`
	SelectedEpisodes        []int           `json:"selected_episodes,omitempty"`
	DirectoryID             string          `json:"directory_id"`
	DownloadPath            string          `json:"download_path"`
	DirectorySnapshot       string          `json:"directory_snapshot,omitempty"`
	MaterialRef             string          `json:"material_ref,omitempty"`
	MaterialState           string          `json:"material_state"`
	Purpose                 string          `json:"purpose"`
	DownloaderSnapshot      string          `json:"downloader_snapshot"`
	ImportMode              string          `json:"import_mode"`
	Request                 SearchRequest   `json:"request"`
	ID                      string          `json:"id"`
	WorkflowID              string          `json:"workflow_id"`
	Media                   catalog.Media   `json:"media"`
	Season                  int             `json:"season"`
	Episodes                []int           `json:"episodes"`
	SiteID                  string          `json:"site_id"`
	ResourceID              string          `json:"resource_id"`
	ResourceTitle           string          `json:"resource_title"`
	DownloaderID            string          `json:"downloader_id"`
	RootID                  string          `json:"root_id"`
	Hash                    string          `json:"hash"`
	State                   string          `json:"state"`
	Progress                float64         `json:"progress"`
	Error                   string          `json:"error"`
	SubscriptionID          string          `json:"subscription_id"`
	SubscriptionRevision    int             `json:"subscription_revision"`
	Auto                    bool            `json:"auto"`
	CreatedAt               string          `json:"created_at"`
	UpdatedAt               string          `json:"updated_at"`
	ImportPlanID            string          `json:"import_plan_id"`
	PreviewID               string          `json:"preview_id"`
}
type Asset struct {
	Part                string        `json:"part,omitempty"`
	IdentitySource      string        `json:"identity_source,omitempty"`
	IdentityConfirmedAt string        `json:"identity_confirmed_at,omitempty"`
	ID                  string        `json:"id"`
	Media               catalog.Media `json:"media"`
	Season              int           `json:"season"`
	Episodes            []int         `json:"episodes"`
	RootID              string        `json:"root_id"`
	Path                string        `json:"path"`
	SourcePath          string        `json:"source_path"`
	Size                int64         `json:"size"`
	State               string        `json:"state"`
	ScrapeState         string        `json:"scrape_state"`
	Renaming            *bool         `json:"renaming,omitempty"`
	FNOSState           string        `json:"fnos_state"`
	FNOSGUID            string        `json:"fnos_guid"`
	FNOSScanAt          string        `json:"fnos_scan_at,omitempty"`
	FNOSScanScope       string        `json:"fnos_scan_scope,omitempty"`
	Error               string        `json:"error"`
	IntentID            string        `json:"intent_id"`
	CreatedAt           string        `json:"created_at"`
	MissingSeenAt       string        `json:"missing_seen_at"`
}
type PlanFile struct {
	DirectoryDigest         string `json:"directory_digest,omitempty"`
	MaterialKind            string `json:"material_kind,omitempty"`
	OriginalPreviewCallback bool   `json:"original_preview_callback,omitempty"`
	OriginalTransferHistory bool   `json:"original_transfer_history,omitempty"`
	ParentFileID            string `json:"parent_file_id,omitempty"`
	HistorySource           string `json:"history_source,omitempty"`
	Part                    string `json:"part,omitempty"`
	Method                  string `json:"method"`
	ID                      string `json:"id"`
	Source                  string `json:"source"`
	Target                  string `json:"target"`
	Size                    int64  `json:"size"`
	ModNS                   int64  `json:"mod_ns"`
	Device                  uint64 `json:"device"`
	Inode                   uint64 `json:"inode"`
	Season                  int    `json:"season"`
	Episodes                []int  `json:"episodes"`
	Status                  string `json:"status"`
	Reason                  string `json:"reason"`
	AssetID                 string `json:"asset_id"`
}
type ImportPlan struct {
	DirectorySourceVersion int           `json:"directory_source_version,omitempty"`
	Manual                 bool          `json:"manual,omitempty"`
	ReorganizeAssetID      string        `json:"reorganize_asset_id,omitempty"`
	Renaming               *bool         `json:"renaming,omitempty"`
	Scraping               *bool         `json:"scraping,omitempty"`
	LibraryTypeFolder      *bool         `json:"library_type_folder,omitempty"`
	Warnings               []string      `json:"warnings"`
	SpaceNeeded            int64         `json:"space_needed"`
	SpaceAvailable         int64         `json:"space_available"`
	ID                     string        `json:"id"`
	WorkflowID             string        `json:"workflow_id"`
	Media                  catalog.Media `json:"media"`
	RootID                 string        `json:"root_id"`
	Mode                   string        `json:"mode"`
	Files                  []PlanFile    `json:"files"`
	State                  string        `json:"state"`
	IntentID               string        `json:"intent_id"`
	CreatedAt              string        `json:"created_at"`
}
type Job struct {
	ParentJobID  string          `json:"parent_job_id,omitempty"`
	RequestID    string          `json:"request_id,omitempty"`
	ID           string          `json:"id"`
	WorkflowID   string          `json:"workflow_id"`
	Kind         string          `json:"kind"`
	State        string          `json:"state"`
	Payload      json.RawMessage `json:"payload"`
	Attempt      int             `json:"attempt"`
	Generation   int             `json:"generation"`
	RunAt        int64           `json:"run_at"`
	LeaseUntil   int64           `json:"lease_until"`
	ErrorCode    string          `json:"error_code"`
	ErrorMessage string          `json:"error_message"`
	CreatedAt    string          `json:"created_at"`
	UpdatedAt    string          `json:"updated_at"`
}
type Event struct {
	Seq        int64  `json:"seq"`
	Time       string `json:"time"`
	WorkflowID string `json:"workflow_id"`
	JobID      string `json:"job_id"`
	Level      string `json:"level"`
	Module     string `json:"module"`
	Event      string `json:"event"`
	Message    string `json:"message"`
}
type Candidate struct {
	originalSelectionContext json.RawMessage
	originalSelectionMeta    json.RawMessage
	originalSelectionAllowed *[]int
	originalFilterModel      *originalTitleFilterModel
	NeedsConfirmation        bool           `json:"needs_confirmation"`
	Resource                 sites.Resource `json:"resource"`
	Accepted                 bool           `json:"accepted"`
	Reasons                  []string       `json:"reasons"`
	Season                   int            `json:"season"`
	Episodes                 []int          `json:"episodes"`
}
type SearchOutput struct {
	Candidates []Candidate  `json:"candidates"`
	Sites      []SiteStatus `json:"sites"`
	WorkflowID string       `json:"workflow_id"`
	CachedAt   string       `json:"cached_at"`
}
type SiteStatus struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	Count     int    `json:"count"`
	ElapsedMS int64  `json:"elapsed_ms"`
}
type DownloadPreview struct {
	SourceDownloader   json.RawMessage `json:"source_downloader,omitempty"`
	SeasonScopeSHA     string          `json:"season_scope_sha,omitempty"`
	SelectedEpisodes   []int           `json:"selected_episodes,omitempty"`
	DirectoryID        string          `json:"directory_id"`
	DownloadPath       string          `json:"download_path"`
	DirectorySnapshot  string          `json:"directory_snapshot,omitempty"`
	MaterialRef        string          `json:"material_ref,omitempty"`
	MaterialState      string          `json:"material_state"`
	DownloaderSnapshot string          `json:"downloader_snapshot"`
	ImportMode         string          `json:"import_mode"`
	WorkflowID         string          `json:"workflow_id"`
	Request            SearchRequest   `json:"request"`
	Warnings           []string        `json:"warnings"`
	NeedsConfirmation  bool            `json:"needs_confirmation"`
	ID                 string          `json:"id"`
	Media              catalog.Media   `json:"media"`
	Season             int             `json:"season"`
	Episodes           []int           `json:"episodes"`
	SiteID             string          `json:"site_id"`
	ResourceID         string          `json:"resource_id"`
	ResourceTitle      string          `json:"resource_title"`
	DownloaderID       string          `json:"downloader_id"`
	RootID             string          `json:"root_id"`
	Hash               string          `json:"hash"`
	Files              []string        `json:"files"`
	TotalBytes         int64           `json:"total_bytes"`
	ExtraFiles         bool            `json:"extra_files"`
	CreatedAt          string          `json:"created_at"`
	ExpiresAt          string          `json:"expires_at"`
	Policy             Policy          `json:"policy"`
	SubscriptionID     string          `json:"subscription_id"`
	Revision           int             `json:"revision"`
	Auto               bool            `json:"auto"`
}

func mediaKey(m catalog.Media) string       { return fmt.Sprintf("%s:%d", m.MediaType, m.ID) }
func validMedia(kind string, id int64) bool { return (kind == "movie" || kind == "tv") && id > 0 }
func defaultSchedule() Schedule {
	return Schedule{Source: "metadata", Rule: "weekly", Timezone: "Asia/Shanghai", Mode: "follow", IntervalMinutes: 360, WindowHours: 6, ActiveMinutes: 30, FallbackMinutes: 360, WaitMinutes: 30, EveryDays: 1, StartDate: time.Now().Format("2006-01-02")}
}

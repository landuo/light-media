// Package download controls one explicitly configured downloader instance.
// It never deletes downloaded data and never follows HTTP redirects.
package download

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"light-media/internal/logging"
)

const maxResponse = 16 << 20

var (
	instanceID  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	hashValue   = regexp.MustCompile(`^(?:[a-fA-F0-9]{40}|[a-fA-F0-9]{64})$`)
	ErrNotFound = errors.New("download task not found")
)

type Config struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	BaseURL      string `json:"base_url"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	AutoCategory bool   `json:"auto_category,omitempty"`
	Sequential   bool   `json:"sequential,omitempty"`
	FirstLast    bool   `json:"first_last_piece,omitempty"`
	ForceResume  bool   `json:"force_resume,omitempty"`
}

type Task struct {
	ID            string   `json:"id"`
	Hash          string   `json:"hash"`
	Name          string   `json:"name"`
	InstanceID    string   `json:"instance_id"`
	Progress      float64  `json:"progress"`
	Status        string   `json:"status"`
	DownloadDir   string   `json:"download_dir"`
	TotalSize     int64    `json:"total_size"`
	Completed     int64    `json:"completed"`
	Ratio         float64  `json:"ratio"`
	ETA           int64    `json:"eta"`
	AddedAt       int64    `json:"added_at"`
	DownloadSpeed int64    `json:"download_speed"`
	UploadSpeed   int64    `json:"upload_speed"`
	Tags          []string `json:"tags"`
}

type File struct {
	Path      string  `json:"path"`
	Size      int64   `json:"size"`
	Completed int64   `json:"completed"`
	Progress  float64 `json:"progress"`
}

type AddRequest struct {
	Torrent  []byte   `json:"-"`
	SavePath string   `json:"save_path"`
	Paused   bool     `json:"paused"`
	Tags     []string `json:"tags,omitempty"`
}

type AddResult struct {
	Hash          string `json:"hash"`
	Duplicate     bool   `json:"duplicate"`
	sourceReceipt *sourceAddReceipt
}

// Error deliberately excludes remote bodies, URLs, headers and credentials.
type Error struct{ Operation, Code string }

func (e *Error) Error() string      { return "downloader " + e.Operation + ": " + e.Code }
func failure(op, code string) error { return &Error{op, code} }

type Client struct {
	config    Config
	base      *url.URL
	http      *http.Client
	mu        sync.Mutex
	loggedIn  bool
	trDialect string
	trSession string
	requestID uint64
}

// New may be called by separate request handlers. Serialize adds to the same
// endpoint across clients so their existence checks cannot race in this process.
var endpointAddLocks sync.Map

func New(config Config) (*Client, error) {
	if !instanceID.MatchString(config.ID) {
		return nil, failure("configure", "invalid_instance_id")
	}
	switch config.Kind {
	case "qb", "qbittorrent":
		config.Kind = "qbittorrent"
	case "tr", "transmission":
		config.Kind = "transmission"
	default:
		return nil, failure("configure", "unsupported_kind")
	}
	u, err := url.Parse(config.BaseURL)
	if err != nil || u.Host == "" || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, failure("configure", "invalid_base_url")
	}
	if strings.ContainsAny(u.Path, "\x00\r\n\\") || strings.Contains(u.EscapedPath(), "%") {
		return nil, failure("configure", "invalid_base_path")
	}
	if len(config.Username) > 256 || len(config.Password) > 4096 || strings.ContainsAny(config.Username, "\r\n:") {
		return nil, failure("configure", "invalid_credentials")
	}
	config.APIKey = strings.TrimSpace(config.APIKey)
	if len(config.APIKey) > 4096 || strings.IndexFunc(config.APIKey, func(r rune) bool { return r < 32 || r == 127 }) >= 0 || (config.APIKey != "" && config.Kind != "qbittorrent") {
		return nil, failure("configure", "invalid_api_key")
	}
	if config.Kind == "transmission" && (u.Path == "" || u.Path == "/") {
		u.Path = "/transmission/rpc"
	}
	u.Path = strings.TrimRight(u.Path, "/")
	jar, _ := cookiejar.New(nil)
	h := &http.Client{Timeout: 20 * time.Second, Jar: jar, Transport: logging.Transport(http.DefaultTransport), CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Client{config: config, base: u, http: h}, nil
}

func (c *Client) List(ctx context.Context) ([]Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.config.Kind == "qbittorrent" {
		return c.qbList(ctx, "")
	}
	if err := c.trConnect(ctx); err != nil {
		return nil, err
	}
	return c.trList(ctx, "")
}

// ListDownloading follows MoviePilot's download page scope. qBittorrent owns
// its downloading filter; Transmission uses the download queue / downloading
// states (3 / 4), excluding finished, seeding, stopped and checking torrents.
// The caller separately limits these results to application-owned torrents.
func (c *Client) ListDownloading(ctx context.Context) ([]Task, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.config.Kind == "qbittorrent" {
		return c.qbListQuery(ctx, "", "downloading")
	}
	if err := c.trConnect(ctx); err != nil {
		return nil, err
	}
	return c.trListQuery(ctx, "", true)
}

func (c *Client) Files(ctx context.Context, hash string) ([]File, error) {
	hash, err := c.validHash(hash)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.config.Kind == "qbittorrent" {
		return c.qbFiles(ctx, hash)
	}
	if err := c.trConnect(ctx); err != nil {
		return nil, err
	}
	return c.trFiles(ctx, hash)
}

// DefaultSavePath reads the daemon's own setting. The application never changes
// daemon preferences; an empty directory choice is resolved before submission.
func (c *Client) DefaultSavePath(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var destination string
	if c.config.Kind == "qbittorrent" {
		body, err := c.qb(ctx, http.MethodGet, "app/preferences", "", "")
		if err != nil {
			return "", err
		}
		var settings struct {
			SavePath string `json:"save_path"`
		}
		if err := decode(body, &settings); err != nil {
			return "", err
		}
		destination = settings.SavePath
	} else {
		if err := c.trConnect(ctx); err != nil {
			return "", err
		}
		settings, err := c.trRPC(ctx, "session_get", map[string]any{}, c.trDialect)
		if err != nil {
			return "", err
		}
		if json.Unmarshal(settings["download_dir"], &destination) != nil {
			_ = json.Unmarshal(settings["download-dir"], &destination)
		}
	}
	destination = strings.TrimRight(destination, "/")
	if !absolutePath(destination) {
		return "", failure("preferences", "invalid_default_save_path")
	}
	return destination, nil
}

// Add validates exact metainfo bytes and checks for an existing hash first.
// A duplicate never changes its download directory, tags or running state.
// Uncertain transport failures are not automatically retried.
func (c *Client) Add(ctx context.Context, in AddRequest) (AddResult, error) {
	if !absolutePath(in.SavePath) {
		return AddResult{}, beforeMutation(failure("add", "invalid_save_path"))
	}
	if !validTags(in.Tags) {
		return AddResult{}, beforeMutation(failure("add", "invalid_tags"))
	}
	hashes, err := torrentHashes(in.Torrent, c.config.Kind)
	if err != nil {
		return AddResult{}, beforeMutation(err)
	}
	key := c.config.Kind + "|" + c.base.Scheme + "|" + strings.ToLower(c.base.Host) + c.base.Path
	shared, _ := endpointAddLocks.LoadOrStore(key, new(sync.Mutex))
	addLock := shared.(*sync.Mutex)
	addLock.Lock()
	defer addLock.Unlock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.config.Kind == "qbittorrent" {
		result, err := c.qbAdd(ctx, in, hashes)
		return c.sourceAddResult(result, in.Paused, err)
	}
	hash := hashes[0]
	if err := c.trConnect(ctx); err != nil {
		return AddResult{Hash: hash}, beforeMutation(err)
	}
	result, err := c.trAdd(ctx, in, hash)
	return c.sourceAddResult(result, in.Paused, err)
}

// Control accepts exactly one hash, never wildcard selectors or numeric daemon IDs.
// remove_keep_data preserves files; remove_with_data explicitly deletes them.
func (c *Client) Control(ctx context.Context, hash, action string) error {
	hash, err := c.validHash(hash)
	if err != nil {
		return beforeMutation(err)
	}
	switch action {
	case "start", "pause", "verify", "remove_keep_data", "remove_with_data":
	default:
		return beforeMutation(failure("control", "unsupported_action"))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.config.Kind == "qbittorrent" {
		return c.qbControl(ctx, hash, action)
	}
	if err := c.trConnect(ctx); err != nil {
		return beforeMutation(err)
	}
	return c.trControl(ctx, hash, action)
}

// Relocate requests an explicit data move to a new absolute downloader path.
// It is intentionally separate from Control so a destination cannot be implicit.
func (c *Client) Relocate(ctx context.Context, hash, destination string) error {
	hash, err := c.validHash(hash)
	if err != nil {
		return err
	}
	if !absolutePath(destination) {
		return failure("relocate", "invalid_destination")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.config.Kind == "qbittorrent" {
		if err := c.qbExists(ctx, hash); err != nil {
			return err
		}
		_, err = c.qb(ctx, http.MethodPost, "torrents/setLocation", url.Values{"hashes": {hash}, "location": {destination}}.Encode(), "application/x-www-form-urlencoded")
		return err
	}
	if err := c.trConnect(ctx); err != nil {
		return err
	}
	if err := c.trExists(ctx, hash); err != nil {
		return err
	}
	_, err = c.trRPC(ctx, "torrent_set_location", map[string]any{"ids": []string{hash}, "location": destination, "move": true}, c.trDialect)
	return err
}

func (c *Client) validHash(value string) (string, error) {
	// A compound identity must belong to this client instance.
	if strings.Contains(value, ":") {
		prefix, h, ok := strings.Cut(value, ":")
		if !ok || prefix != c.config.ID {
			return "", failure("select", "wrong_instance")
		}
		value = h
	}
	if !hashValue.MatchString(value) {
		return "", failure("select", "invalid_hash")
	}
	return strings.ToLower(value), nil
}

func absolutePath(value string) bool {
	return len(value) > 0 && len(value) <= 4096 && strings.HasPrefix(value, "/") && !strings.ContainsAny(value, "\x00\r\n\\") && path.Clean(value) == value
}
func relativePath(value string) bool {
	return len(value) > 0 && len(value) <= 4096 && !strings.HasPrefix(value, "/") && !strings.ContainsAny(value, "\x00\r\n\\") && value != "." && value != ".." && !strings.HasPrefix(value, "../") && path.Clean(value) == value
}
func validTags(tags []string) bool {
	if len(tags) > 32 {
		return false
	}
	for _, t := range tags {
		if len(t) == 0 || len(t) > 128 || strings.TrimSpace(t) != t || strings.ContainsAny(t, ",\x00\r\n") {
			return false
		}
	}
	return true
}
func fraction(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 1 }
func decode(data []byte, to any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	if err := d.Decode(to); err != nil {
		return failure("response", "invalid_json")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return failure("response", "invalid_json_tail")
	}
	return nil
}
func (c *Client) request(ctx context.Context, method string, endpoint *url.URL, body []byte, contentType, session string) ([]byte, http.Header, int, error) {
	ctx, _ = logging.Target(ctx, "downloader")
	req, err := http.NewRequestWithContext(logging.EndpointContext(ctx, "integration", endpoint.String()), method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, nil, 0, failure("request", "invalid_request")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json, text/plain")
	req.Header.Set("Referer", c.base.Scheme+"://"+c.base.Host+"/")
	if c.config.Kind == "qbittorrent" && c.config.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}
	if c.config.Kind == "transmission" {
		req.SetBasicAuth(c.config.Username, c.config.Password)
		if session != "" {
			req.Header.Set("X-Transmission-Session-Id", session)
		}
	}
	res, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, 0, ctx.Err()
		}
		return nil, nil, 0, failure("request", "transport_failed_outcome_unknown")
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return nil, nil, res.StatusCode, failure("response", "body_read_failed_outcome_unknown")
	}
	if len(data) > maxResponse {
		return nil, nil, res.StatusCode, failure("response", "body_limit")
	}
	return data, res.Header, res.StatusCode, nil
}
func statusError(status int) error { return failure("request", fmt.Sprintf("http_%d", status)) }

// metadata-nfo validates a supplied fixed-sample report and encodes local NFO.
// It never calls metadata.Run, a provider API, a downloader or a NAS.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"light-media/internal/media"
	"light-media/internal/metadata"
)

const maxInput = 1 << 20

var reportKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

type inputReport struct {
	metadata.Report
	RunID   string      `json:"run_id,omitempty"`
	Logging *logSummary `json:"logging,omitempty"`
}
type logSummary struct {
	Status         string `json:"status"`
	Written        int64  `json:"written"`
	Failed         int64  `json:"failed"`
	Dropped        int64  `json:"dropped"`
	AttemptedBytes int64  `json:"attempted_bytes"`
	BudgetBytes    int64  `json:"budget_bytes"`
}
type sampleSpec struct {
	name, kind, title, path, showTitle string
	id                                 int64
	season, episode                    int
}

var specs = []sampleSpec{
	{"movie-nightking", "movie", "夜王", "夜王 (2026)/夜王 (2026).nfo", "", 1602727, 0, 0},
	{"series-bigbang", "tv", "生活大爆炸", "生活大爆炸/tvshow.nfo", "", 1418, 0, 0},
	{"episode-bigbang", "episode", "", "生活大爆炸/Season 7/生活大爆炸 S07E17.nfo", "生活大爆炸", 1418, 7, 17},
	{"series-zhuxian", "tv", "诛仙", "诛仙/tvshow.nfo", "", 206484, 0, 0},
	{"episode-zhuxian", "episode", "", "诛仙/Season 4/诛仙 S04E05.nfo", "诛仙", 206484, 4, 5},
}

type plannedFile struct {
	Sample    string `json:"sample"`
	Path      string `json:"relative_path"`
	SourceKey string `json:"source_key"`
	ParentKey string `json:"parent_source_key,omitempty"`
	Season    int    `json:"season,omitempty"`
	Episode   int    `json:"episode,omitempty"`
	Date      string `json:"date"`
	Year      int    `json:"year"`
	Bytes     int    `json:"bytes"`
	SHA256    string `json:"sha256"`
	content   []byte
}
type plan struct {
	Status                  string        `json:"status"`
	Validation              string        `json:"validation"`
	InputEvidence           string        `json:"input_evidence"`
	InputSHA256             string        `json:"input_sha256"`
	InputSourceAuthenticity string        `json:"input_source_authenticity"`
	FNOSReadback            string        `json:"fnos_readback"`
	SyntheticInput          bool          `json:"synthetic_input"`
	NetworkRequests         int           `json:"network_requests"`
	NFOCount                int           `json:"nfo_count"`
	VideoCount              int           `json:"video_count"`
	ImageCount              int           `json:"image_count"`
	ActorCount              int           `json:"actor_count"`
	Files                   []plannedFile `json:"files"`
}

// Reject duplicate JSON object keys before struct decoding, so contradictory
// status/evidence declarations cannot be resolved by last-key-wins decoding.
func uniqueKeys(d *json.Decoder, depth int) error {
	if depth > 16 {
		return errors.New("INVALID_REPORT")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			// encoding/json also accepts case-folded aliases for struct tags;
			// require the report's canonical lowercase ASCII key spelling.
			if !ok || !reportKey.MatchString(name) || seen[name] {
				return errors.New("INVALID_REPORT")
			}
			seen[name] = true
		}
		if err := uniqueKeys(d, depth+1); err != nil {
			return err
		}
	}
	_, err = d.Token()
	return err
}

func parseReport(body []byte) (inputReport, error) {
	var report inputReport
	if len(body) == 0 || len(body) > maxInput {
		return report, errors.New("INVALID_REPORT")
	}
	d := json.NewDecoder(bytes.NewReader(body))
	if uniqueKeys(d, 0) != nil || d.Decode(new(any)) != io.EOF {
		return report, errors.New("INVALID_REPORT")
	}
	d = json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(&report) != nil || d.Decode(new(any)) != io.EOF {
		return report, errors.New("INVALID_REPORT")
	}
	return report, nil
}

func safeTitle(s string) bool {
	if s == "" || len(s) > 512 || !utf8.ValidString(s) || strings.Contains(s, "://") {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func buildPlan(body []byte) (plan, error) {
	report, err := parseReport(body)
	if err != nil {
		return plan{}, err
	}
	if report.Status != "ok" || report.Requests != 5 || len(report.Results) != 5 || (report.Evidence != "live_request" && report.Evidence != "synthetic_http_roundtrip") {
		return plan{}, errors.New("REPORT_NOT_FULLY_VERIFIED")
	}
	if l := report.Logging; l != nil && (l.Status != "ok" || l.Failed != 0 || l.Dropped != 0 || l.Written < 0 || l.AttemptedBytes < 0 || l.BudgetBytes < 0) {
		return plan{}, errors.New("REPORT_LOGGING_FAILED")
	}
	bySample := map[string]metadata.Result{}
	for _, result := range report.Results {
		if _, duplicate := bySample[result.Sample]; duplicate {
			return plan{}, errors.New("DUPLICATE_OR_UNKNOWN_SAMPLE")
		}
		bySample[result.Sample] = result
	}
	seenKeys := map[string]bool{}
	for _, s := range specs {
		r, exists := bySample[s.name]
		if !exists {
			return plan{}, errors.New("DUPLICATE_OR_UNKNOWN_SAMPLE")
		}
		if r.Status != "ok" || r.Reason != "" || r.Source != "tmdb" || r.Type != s.kind || r.RequestedSourceID != s.id || r.SourceID <= 0 || !safeTitle(r.Title) {
			return plan{}, errors.New("INVALID_SAMPLE_IDENTITY")
		}
		date, e := time.Parse("2006-01-02", r.Date)
		if e != nil || date.Format("2006-01-02") != r.Date || r.Year != date.Year() || r.Year < 1800 || r.Year > 2199 {
			return plan{}, errors.New("INVALID_SAMPLE_DATE")
		}
		if s.kind == "episode" {
			if r.ParentSourceID != s.id || r.Season != s.season || r.Episode != s.episode || (r.ParentContext != "verified_series_and_fixed_request_route" && r.ParentContext != "verified_response_show_id_and_request_route") {
				return plan{}, errors.New("INVALID_EPISODE_CONTEXT")
			}
		} else if r.SourceID != s.id || r.Title != s.title || r.ParentSourceID != 0 || r.ParentContext != "" || r.Season != 0 || r.Episode != 0 {
			return plan{}, errors.New("INVALID_SAMPLE_IDENTITY")
		}
		if s.kind == "movie" && r.Year != 2026 {
			return plan{}, errors.New("INVALID_SAMPLE_DATE")
		}
		key := "tmdb:" + r.Type + ":" + strconv.FormatInt(r.SourceID, 10)
		if seenKeys[key] {
			return plan{}, errors.New("DUPLICATE_SOURCE_IDENTITY")
		}
		seenKeys[key] = true
	}
	inputHash := sha256.Sum256(body)
	out := plan{Status: "dry_run", Validation: "supplied_validated_report", InputEvidence: report.Evidence, InputSHA256: hex.EncodeToString(inputHash[:]), InputSourceAuthenticity: "not_independently_verified", FNOSReadback: "unverified", SyntheticInput: report.Evidence == "synthetic_http_roundtrip", NFOCount: 5, Files: make([]plannedFile, 0, 5)}
	for _, s := range specs {
		r := bySample[s.name]
		kind := s.kind
		if kind == "tv" {
			kind = "tvshow"
		}
		// SourceID is the result's own typed identity. RequestedSourceID and
		// ParentSourceID identify the show route, never an episode uniqueid.
		m := media.NFOMetadata{Kind: kind, Title: r.Title, Year: r.Year, Season: r.Season, Episode: r.Episode, ShowTitle: s.showTitle, UniqueIDs: []media.UniqueID{{Type: "tmdb", Default: true, Value: strconv.FormatInt(r.SourceID, 10)}}}
		if kind == "episode" {
			m.Aired = r.Date
		}
		content, e := media.EncodeNFO(m)
		if e != nil {
			return plan{}, errors.New("NFO_ENCODE_FAILED")
		}
		hash := sha256.Sum256(content)
		file := plannedFile{Sample: s.name, Path: s.path, SourceKey: "tmdb:" + r.Type + ":" + strconv.FormatInt(r.SourceID, 10), Season: r.Season, Episode: r.Episode, Date: r.Date, Year: r.Year, Bytes: len(content), SHA256: hex.EncodeToString(hash[:]), content: content}
		if r.Type == "episode" {
			file.ParentKey = "tmdb:tv:" + strconv.FormatInt(r.ParentSourceID, 10)
		}
		out.Files = append(out.Files, file)
	}
	return out, nil
}

var directoryName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$`)

func applyPlan(p *plan, output, work string) error {
	if output == "" {
		return errors.New("EXPLICIT_NEW_OUTPUT_REQUIRED")
	}
	abs, err := filepath.Abs(output)
	if err != nil {
		return errors.New("INVALID_OUTPUT_DIRECTORY")
	}
	work, err = filepath.Abs(work)
	if err != nil || filepath.Dir(abs) != work || !directoryName.MatchString(filepath.Base(abs)) {
		return errors.New("OUTPUT_MUST_BE_NEW_DIRECT_CHILD_OF_WORK")
	}
	resolved, err := filepath.EvalSymlinks(work)
	if err != nil || resolved != work {
		return errors.New("WORK_MISSING_OR_SYMLINK")
	}
	parent, err := os.OpenRoot(work)
	if err != nil {
		return errors.New("WORK_UNAVAILABLE")
	}
	defer parent.Close()
	name := filepath.Base(abs)
	if parent.Mkdir(name, 0700) != nil {
		return errors.New("OUTPUT_EXISTS_OR_UNAVAILABLE")
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return errors.New("OUTPUT_WRITE_FAILED")
	}
	defer root.Close()
	for _, file := range p.Files {
		local := filepath.FromSlash(file.Path)
		if root.MkdirAll(filepath.Dir(local), 0700) != nil {
			return errors.New("OUTPUT_WRITE_FAILED")
		}
		f, err := root.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return errors.New("OUTPUT_WRITE_FAILED")
		}
		n, writeErr := f.Write(file.content)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil || n != len(file.content) {
			return errors.New("OUTPUT_WRITE_FAILED")
		}
	}
	p.Status = "generated"
	return nil
}

func runCLI(args []string, input io.Reader, output io.Writer, work string) int {
	fail := func(code string, exit int) int {
		_ = json.NewEncoder(output).Encode(map[string]string{"status": "failed", "code": code})
		return exit
	}
	flags := flag.NewFlagSet("metadata-nfo", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	apply := flags.Bool("apply", false, "create only five NFO files in an exclusive new local directory")
	outDir := flags.String("out-dir", "", "explicit new direct child of project work directory")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return fail("INVALID_FLAGS", 2)
	}
	body, err := io.ReadAll(io.LimitReader(input, maxInput+1))
	if err != nil || len(body) > maxInput {
		return fail("INVALID_REPORT", 2)
	}
	p, err := buildPlan(body)
	if err != nil {
		return fail(err.Error(), 1)
	}
	if *apply {
		if err := applyPlan(&p, *outDir, work); err != nil {
			return fail(err.Error(), 1)
		}
	}
	if json.NewEncoder(output).Encode(p) != nil {
		return 1
	}
	return 0
}

func main() { os.Exit(runCLI(os.Args[1:], os.Stdin, os.Stdout, "work")) }

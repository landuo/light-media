package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"light-media/internal/media"
)

type input struct {
	Evidence string   `json:"evidence"`
	Samples  []sample `json:"samples"`
}
type sample struct {
	Name       string              `json:"name"`
	Filename   string              `json:"filename"`
	MediaID    string              `json:"media_id"`
	Source     media.Source        `json:"source,omitempty"`
	Rules      []media.MappingRule `json:"rules,omitempty"`
	Known      []media.Episode     `json:"known_episodes,omitempty"`
	Correction []media.Episode     `json:"correction,omitempty"`
	NFO        *media.NFOMetadata  `json:"nfo,omitempty"`
}
type result struct {
	Name    string               `json:"name"`
	Parse   media.ParseResult    `json:"parse"`
	Mapping *media.MappingResult `json:"mapping,omitempty"`
	NFO     string               `json:"nfo,omitempty"`
	NFOFile string               `json:"nfo_file,omitempty"`
	Error   string               `json:"error,omitempty"`
}

func main() { os.Exit(run()) }

func run() int {
	outDir := flag.String("out-dir", "", "explicit temporary work directory; NFO files use exclusive creation")
	flag.Parse()
	if flag.NArg() != 0 {
		return fail("unexpected_arguments")
	}
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20))
	decoder.DisallowUnknownFields()
	var in input
	if err := decoder.Decode(&in); err != nil {
		return fail("invalid_input_json")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fail("multiple_json_values_or_oversized_input")
	}
	if (in.Evidence != "synthetic" && in.Evidence != "real_sample") || len(in.Samples) == 0 || len(in.Samples) > 100 {
		return fail("evidence_and_1_to_100_samples_required")
	}
	if *outDir != "" {
		info, err := os.Stat(*outDir)
		if err != nil || !info.IsDir() {
			return fail("output_directory_must_exist")
		}
	}
	validName := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
	seen := map[string]bool{}
	for _, s := range in.Samples {
		if !validName.MatchString(s.Name) || seen[s.Name] {
			return fail("unique_safe_sample_names_required")
		}
		seen[s.Name] = true
	}
	results, exitCode := make([]result, 0, len(in.Samples)), 0
	for _, s := range in.Samples {
		r := result{Name: s.Name, Parse: media.Parse(s.Filename)}
		if r.Parse.Status != media.Parsed {
			exitCode = 2
			results = append(results, r)
			continue
		}
		if r.Parse.Original != nil {
			m := media.Resolve(s.MediaID, *r.Parse.Original, s.Source, s.Rules, s.Known, s.Correction)
			r.Mapping = &m
			if m.Status != media.Parsed {
				exitCode = 2
				results = append(results, r)
				continue
			}
		}
		if s.NFO != nil {
			if s.MediaID == "" {
				r.Error = "unconfirmed_media_identity"
			} else if s.NFO.Kind == "episode" && (r.Mapping == nil || len(r.Mapping.Standard) != 1 || r.Mapping.Standard[0] != (media.Episode{Season: s.NFO.Season, Episode: s.NFO.Episode})) {
				r.Error = "nfo_numbering_does_not_match_resolved_single_episode"
			} else if s.NFO.Kind == "movie" && r.Parse.Type != "movie" {
				r.Error = "nfo_kind_does_not_match_file"
			} else if s.NFO.Kind != "movie" && r.Parse.Type != "episode" {
				r.Error = "nfo_kind_does_not_match_file"
			} else {
				data, err := media.EncodeNFO(*s.NFO)
				if err != nil {
					r.Error = "invalid_nfo_metadata"
				} else if *outDir == "" {
					r.NFO = string(data)
				} else {
					filename := s.Name + ".nfo"
					f, err := os.OpenFile(filepath.Join(*outDir, filename), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
					if err != nil {
						r.Error = "nfo_exclusive_create_failed"
					} else {
						_, writeErr := f.Write(data)
						closeErr := f.Close()
						if writeErr != nil || closeErr != nil {
							r.Error = "nfo_write_failed"
						} else {
							r.NFOFile = filename
						}
					}
				}
			}
		}
		if r.Error != "" {
			exitCode = 2
		}
		results = append(results, r)
	}
	status := "passed"
	if exitCode != 0 {
		status = "needs_review"
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Evidence             string   `json:"evidence"`
		Status               string   `json:"status"`
		IdentityVerification string   `json:"identity_verification"`
		FNOSVerification     string   `json:"fnos_verification"`
		Results              []result `json:"results"`
	}{in.Evidence, status, "supplied_identity_only_no_remote_lookup", "not_verified_on_device", results}); err != nil {
		return 1
	}
	return exitCode
}

func fail(code string) int {
	fmt.Fprintf(os.Stdout, "{\"status\":\"failed\",\"reason\":%q}\n", code)
	return 1
}

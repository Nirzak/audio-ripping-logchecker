package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/Nirzak/logchecker-go/accuraterip"
	"github.com/Nirzak/logchecker-go/gnudb"
	"github.com/Nirzak/logchecker-go/logchecker"
)

// ---------------------------------------------------------------------------
// HTML sanitisation — allowlist approach (RE2 has no lookahead support)
// ---------------------------------------------------------------------------

// reAnyTag matches any HTML open/close tag, capturing:
//
//	m[1] = "/" for closing tags, "" for opening
//	m[2] = tag name
//	m[3] = raw attribute string (empty for closing tags)
var reAnyTag = regexp.MustCompile(`(?i)<(/?)(\w+)([^>]*)>`)

// reUnsafeAttrs matches event handlers and navigation-related attributes
// that must be stripped from allowed tags.
var reUnsafeAttrs = regexp.MustCompile(`(?i)\s(?:on\w+|href|src|action|formaction|data-\S*)=[^\s>]*`)

// allowedTags is the set of HTML tags that the logchecker library may emit
// and that are safe to pass through to the browser.
var allowedTags = map[string]bool{
	"span": true, "div": true, "p": true,
	"strong": true, "em": true, "br": true,
}

// sanitizeHTML strips every tag that is not in allowedTags and removes unsafe
// attributes from the tags that remain.
//
// The original negative-lookahead regex ((?i)<(?!/?(span|…)(\s|>))[^>]*>)
// panics at startup because Go's RE2 engine does not support lookaheads.
// This function achieves the same result without lookaheads.
func sanitizeHTML(raw string) string {
	return reAnyTag.ReplaceAllStringFunc(raw, func(tag string) string {
		m := reAnyTag.FindStringSubmatch(tag)
		if m == nil {
			return ""
		}
		name := strings.ToLower(m[2])
		if !allowedTags[name] {
			return ""
		}
		attrs := reUnsafeAttrs.ReplaceAllString(m[3], "")
		if m[1] == "/" {
			return "</" + name + ">"
		}
		return "<" + name + attrs + ">"
	})
}

// ---------------------------------------------------------------------------
// Analysis
// ---------------------------------------------------------------------------

// analysisResult is the structured output of a single log check.
type analysisResult struct {
	Ripper        string   `json:"ripper"`
	RipperVersion string   `json:"ripper_version"`
	Score         int      `json:"score"`
	ChecksumState string   `json:"checksum_state"`
	Details       []string `json:"details"`
	Language      string   `json:"language"`
	IsCombinedLog bool     `json:"is_combined_log"`
	RdbarrRip     string   `json:"rdbarr_rip,omitempty"`
	DiscIDs       []discID `json:"disc_ids,omitempty"`
}

// discBadge is a small status pill shown next to a disc ID (e.g. AccurateRip
// "Found", gnudb "Matched"). State drives the CSS class: "ok", "miss", or
// "unknown".
type discBadge struct {
	Label string `json:"label"`
	State string `json:"state"`
}

// discID is one database identifier for the ripped disc, with an optional
// external lookup URL (rendered as a clickable arrow) and status badge.
type discID struct {
	Key   string     `json:"key"`
	Label string     `json:"label"`
	Value string     `json:"value"`
	URL   string     `json:"url,omitempty"`
	Title string     `json:"title,omitempty"`
	Badge *discBadge `json:"badge,omitempty"`
}

// apiResult is the flat, client-friendly JSON shape returned by POST /api.
// It flattens analysisResult.DiscIDs into individual id/url/status fields so
// consumers don't have to walk a nested array. Empty fields are omitted.
type apiResult struct {
	Ripper        string   `json:"ripper"`
	RipperVersion string   `json:"ripper_version"`
	Score         int      `json:"score"`
	ChecksumState string   `json:"checksum_state"`
	Details       []string `json:"details,omitempty"`
	Language      string   `json:"language"`
	IsCombinedLog bool     `json:"is_combined_log"`
	RdbarrRip     string   `json:"rdbarr_rip,omitempty"`

	MusicBrainzID  string `json:"musicbrainz_id,omitempty"`
	MusicBrainzURL string `json:"musicbrainz_url,omitempty"`
	CTDBID         string `json:"ctdb_id,omitempty"`
	CTDBURL        string `json:"ctdb_url,omitempty"`
	FreeDBID       string `json:"freedb_id,omitempty"`
	AccurateRipID  string `json:"accuraterip_id,omitempty"`
	AccurateRipSt  string `json:"accuraterip_status,omitempty"`
	GnuDBID        string `json:"gnudb_id,omitempty"`
	GnuDBURL       string `json:"gnudb_url,omitempty"`
	GnuDBStatus    string `json:"gnudb_status,omitempty"`
	GnuDBTitle     string `json:"gnudb_title,omitempty"`
}

// toAPIResult flattens an analysisResult into the apiResult wire shape.
func (r *analysisResult) toAPIResult() apiResult {
	out := apiResult{
		Ripper:        r.Ripper,
		RipperVersion: r.RipperVersion,
		Score:         r.Score,
		ChecksumState: r.ChecksumState,
		Details:       r.Details,
		Language:      r.Language,
		IsCombinedLog: r.IsCombinedLog,
		RdbarrRip:     r.RdbarrRip,
	}
	for _, d := range r.DiscIDs {
		switch d.Key {
		case "musicbrainz":
			out.MusicBrainzID, out.MusicBrainzURL = d.Value, d.URL
		case "ctdb":
			out.CTDBID, out.CTDBURL = d.Value, d.URL
		case "freedb":
			out.FreeDBID = d.Value
		case "accuraterip":
			out.AccurateRipID = d.Value
			if d.Badge != nil {
				out.AccurateRipSt = d.Badge.Label
			}
		case "gnudb":
			out.GnuDBID, out.GnuDBURL, out.GnuDBTitle = d.Value, d.URL, d.Title
			if d.Badge != nil {
				out.GnuDBStatus = d.Badge.Label
			}
		}
	}
	return out
}

// collectDiscIDs computes the disc identifiers (MusicBrainz, CTDB, FreeDB,
// AccurateRip, gnudb) from the parsed TOC. The AccurateRip and gnudb database
// lookups hit external servers; they run concurrently under a single shared
// timeout. On timeout / network error the ID is still shown with a neutral
// "unknown" badge.
//
// The TOC type lives in the library's internal package, so it is never named
// here: GetTOC()'s result is used via inference and passed straight through to
// the lookup helpers.
func collectDiscIDs(lc *logchecker.Logchecker) []discID {
	t := lc.GetTOC()
	if t == nil {
		return nil
	}

	var ids []discID
	if id := t.MusicBrainzDiscID(); id != "" {
		ids = append(ids, discID{Key: "musicbrainz", Label: "MusicBrainz", Value: id, URL: t.MusicBrainzLookupURL()})
	}
	if id := t.CTDBDiscID(); id != "" {
		ids = append(ids, discID{Key: "ctdb", Label: "CTDB", Value: id, URL: t.CTDBLookupURL()})
	}
	if id := t.FreeDBDiscID(); id != "" {
		ids = append(ids, discID{Key: "freedb", Label: "FreeDB", Value: id})
	}

	// Concurrent external lookups under a single shared deadline.
	ctx, cancel := context.WithTimeout(context.Background(), discLookupTimeout)
	defer cancel()

	var (
		wg    sync.WaitGroup
		arRes *accuraterip.Result
		arErr error
		gnRes *gnudb.Result
		gnErr error
	)
	wg.Add(2)
	go func() { defer wg.Done(); arRes, arErr = accuraterip.LookupWithContext(ctx, t) }()
	go func() { defer wg.Done(); gnRes, gnErr = gnudb.ResolveWithContext(ctx, t) }()
	wg.Wait()

	// AccurateRip — badge only, no lookup URL exposed in the UI.
	if arID := t.AccurateRipID(); arID != "" {
		badge := &discBadge{Label: "Unknown", State: "unknown"}
		if arErr == nil && arRes != nil {
			switch arRes.Status {
			case accuraterip.StatusFound:
				badge = &discBadge{Label: "Found", State: "ok"}
			case accuraterip.StatusNotFound:
				badge = &discBadge{Label: "Not Found", State: "miss"}
			}
		}
		ids = append(ids, discID{Key: "accuraterip", Label: "AccurateRip", Value: arID, Badge: badge})
	}

	// gnudb — ID + lookup URL + match badge (and album title when matched).
	if gnRes != nil && gnRes.DiscID != "" {
		badge := &discBadge{Label: "Unknown", State: "unknown"}
		if gnErr == nil {
			if gnRes.Matched {
				badge = &discBadge{Label: "Matched", State: "ok"}
			} else {
				badge = &discBadge{Label: "No Match", State: "miss"}
			}
		}
		ids = append(ids, discID{Key: "gnudb", Label: "gnudb", Value: gnRes.DiscID, URL: gnRes.URL, Title: gnRes.Title, Badge: badge})
	}

	return ids
}

// analyze runs the logchecker library against raw file bytes.
// It writes a temporary file (required by the library for checksum validation)
// and removes it when done.
func analyze(raw []byte, origSuffix string, rdbarrRip bool) (*analysisResult, string, error) {
	tmp, err := os.CreateTemp("", "logchecker-*"+origSuffix)
	if err != nil {
		return nil, "", fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return nil, "", fmt.Errorf("write temp file: %w", err)
	}
	tmp.Close()

	lc := logchecker.New()
	if err := lc.NewFile(tmpName); err != nil {
		return nil, "", fmt.Errorf("load file: %w", err)
	}
	lc.Parse()

	score := lc.GetScore()
	htmlLog := lc.GetLog()

	if rdbarrRip {
		score -= 100
		htmlLog = `<div class="bad">Notice: rdbarr rip detected. Score reduced by 100.</div>` + "\n" + htmlLog
	}

	res := &analysisResult{
		Ripper:        lc.GetRipper(),
		RipperVersion: lc.GetRipperVersion(),
		Score:         score,
		ChecksumState: lc.GetChecksumState(),
		Details:       lc.GetDetails(),
		Language:      lc.GetLanguage(),
		IsCombinedLog: lc.IsCombinedLog(),
		DiscIDs:       collectDiscIDs(lc),
	}
	if rdbarrRip {
		res.Details = append(res.Details, "rdbarr rip detected. Score reduced by 100.")
		res.RdbarrRip = "Yes"
	}

	return res, htmlLog, nil
}

// resultToSummary converts an analysisResult into display-friendly key/value pairs
// for the index template.
func resultToSummary(r *analysisResult) []summaryItem {
	ripperDisplay := r.Ripper
	if strings.ToLower(r.Ripper) == "eac" {
		ripperDisplay = "Exact Audio Copy"
	}

	langDisplay := r.Language
	if strings.ToLower(r.Language) == "en" {
		langDisplay = "English"
	}

	combinedDisplay := "No"
	if r.IsCombinedLog {
		combinedDisplay = "Yes"
	}

	items := []summaryItem{
		{Key: "ripper", Value: ripperDisplay},
		{Key: "ripper_version", Value: r.RipperVersion},
		{Key: "score", Value: strconv.Itoa(r.Score)},
		{Key: "checksum_state", Value: r.ChecksumState},
		{Key: "language", Value: langDisplay},
		{Key: "combined_log", Value: combinedDisplay},
	}
	if r.RdbarrRip != "" {
		items = append(items, summaryItem{Key: "rdbarr_rip", Value: r.RdbarrRip})
	}
	return items
}

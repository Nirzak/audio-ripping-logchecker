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
// HTML sanitisation — escape everything, re-emit an exact allowlist
// ---------------------------------------------------------------------------
//
// The logchecker library returns the log text with its own <span class="...">
// markup mixed in, and it does not HTML-escape the log text itself. Whatever a
// user puts in a log (file or pasted text) therefore reaches this function as
// raw HTML. Instead of stripping "bad" tags, sanitizeHTML escapes every byte of
// markup and re-emits only the handful of tags the library actually produces,
// rebuilt from scratch with no attributes other than a known-class list.

// reAllowedOpen matches, at the start of the input, an opening tag the library
// emits: m[1] = tag name, m[2] or m[3] = class list (double / single quoted).
var reAllowedOpen = regexp.MustCompile(`^<(span|div|p|strong|em|br)(?:\s+class\s*=\s*(?:"([a-z0-9 ]*)"|'([a-z0-9 ]*)'))?\s*/?>`)

// reAllowedClose matches, at the start of the input, a closing tag.
var reAllowedClose = regexp.MustCompile(`^</(span|div|p|strong|em)\s*>`)

// allowedClasses is every CSS class the library and this server emit.
var allowedClasses = map[string]bool{
	"good": true, "goodish": true, "bad": true, "badish": true,
	"log1": true, "log2": true, "log3": true, "log4": true, "log5": true,
}

// reMarkup matches anything sanitizeHTML could mistake for library markup.
// Real rip logs never contain these, so a log that does is refused up front:
// otherwise it could colour its own lines "good" in the rendered report.
var reMarkup = regexp.MustCompile(`(?i)</?(?:span|div|p|strong|em|br)[\s/>]`)

// containsMarkup reports whether log text contains HTML-like tags.
func containsMarkup(text string) bool {
	return reMarkup.MatchString(text)
}

// sanitizeHTML returns raw as safe HTML: text is escaped, allowlisted tags are
// rebuilt canonically, stray closing tags are dropped and any tags left open
// are closed at the end.
func sanitizeHTML(raw string) string {
	var b strings.Builder
	b.Grow(len(raw) + len(raw)/8)
	var open []string // stack of open tag names

	for i := 0; i < len(raw); {
		switch raw[i] {
		case '<':
			rest := raw[i:]
			if m := reAllowedOpen.FindStringSubmatch(rest); m != nil {
				if tag, ok := canonicalOpenTag(m[1], m[2]+m[3]); ok {
					b.WriteString(tag)
					if m[1] != "br" {
						open = append(open, m[1])
					}
					i += len(m[0])
					continue
				}
			} else if m := reAllowedClose.FindStringSubmatch(rest); m != nil {
				open = closeTag(&b, open, m[1])
				i += len(m[0])
				continue
			}
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '&':
			b.WriteString("&amp;")
		default:
			b.WriteByte(raw[i])
		}
		i++
	}
	for j := len(open) - 1; j >= 0; j-- {
		b.WriteString("</" + open[j] + ">")
	}
	return b.String()
}

// canonicalOpenTag rebuilds an opening tag, refusing unknown classes.
func canonicalOpenTag(name, classList string) (string, bool) {
	classes := strings.Fields(classList)
	for _, c := range classes {
		if !allowedClasses[c] {
			return "", false
		}
	}
	if len(classes) == 0 {
		return "<" + name + ">", true
	}
	return "<" + name + ` class="` + strings.Join(classes, " ") + `">`, true
}

// closeTag writes the closing tag for name if it is open, first closing any
// tags opened inside it, and returns the updated stack. A closing tag with no
// matching open tag is dropped.
func closeTag(b *strings.Builder, open []string, name string) []string {
	for j := len(open) - 1; j >= 0; j-- {
		if open[j] == name {
			for k := len(open) - 1; k >= j; k-- {
				b.WriteString("</" + open[k] + ">")
			}
			return open[:j]
		}
	}
	return open
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
	Details       []string `json:"details"`
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

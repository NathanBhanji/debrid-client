// Package relname parses scene/release names ("Some.Movie.2019.2160p.WEB-DL.
// x265-GROUP") into structured metadata for library-style organization.
//
// The approach follows the well-trodden PTN/guessit strategy: locate the
// first "boundary" token (year, resolution, source, SxxEyy…) — everything
// before it is the title — then classify the remainder against known-token
// tables. Parsing is deliberately conservative: a name without a confident
// title plus a year or TV marker reports ok=false and callers fall back to
// the raw name.
//
// Known ambiguity: a title-embedded plausible year with no release year
// ("Wonder.Woman.1984.1080p") is indistinguishable from a release year
// without a title database; guessit shares this behavior. The preview UI is
// the mitigation.
package relname

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Info is the parsed release metadata. Zero/absent fields: Year 0,
// Season/Episode -1, empty strings.
type Info struct {
	Title      string
	Year       int
	Season     int
	Episode    int
	EpisodeEnd int // last episode of a multi-episode file (S01E01E02); -1 otherwise
	Resolution string
	Source     string
	Codec      string
	Group      string
	IsTV       bool
}

// media file extensions stripped before parsing (single-file torrents).
var extRe = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|m4v|mov|wmv|mpg|mpeg|webm|flv|iso)$`)

// Boundary and field patterns. All case-insensitive and anchored to token
// edges via separator classes rather than \b (names use . _ - as spaces).
const sep = `[ ._\-\[\](),]`

var (
	// S01E02, S01E02E03, S01E02-E03, S01 (season capped at 2 digits,
	// episode at 3; 4-digit date-style seasons are not supported).
	sxeRe      = regexp.MustCompile(`(?i)(?:^|` + sep + `)S(\d{1,2})[ ._\-]?E(\d{1,3})(?:[E\-](\d{1,3}))*(?:$|` + sep + `)`)
	seasonRe   = regexp.MustCompile(`(?i)(?:^|` + sep + `)S(\d{1,2})(?:$|` + sep + `)`)
	nxnRe      = regexp.MustCompile(`(?i)(?:^|` + sep + `)(\d{1,2})x(\d{2,3})(?:$|` + sep + `)`)
	yearCandRe = regexp.MustCompile(`(?:19|20)\d{2}`)

	resolutionRe = regexp.MustCompile(`(?i)(?:^|` + sep + `)(\d{3,4}[pi]|4k|uhd)(?:$|` + sep + `)`)
	sourceRe     = regexp.MustCompile(`(?i)(?:^|` + sep + `)(blu[ ._\-]?ray|bd[ ._\-]?rip|br[ ._\-]?rip|web[ ._\-]?dl|web[ ._\-]?rip|webmux|web|hdtv|dvd[ ._\-]?rip|dvdscr|dvd|remux|hd[ ._\-]?rip|hdcam|cam[ ._\-]?rip|cam|telesync|telecine|screener|scr|vhs)(?:$|` + sep + `)`)
	codecRe      = regexp.MustCompile(`(?i)(?:^|` + sep + `)(x[ ._]?26[45]|h[ ._]?26[45]|hevc|avc|av1|xvid|divx|vp9)(?:$|` + sep + `)`)

	// Trailing release group: "-GROUP" (optionally followed by a bracket tag
	// like [rartv] or [eztv.re]) at the very end of the extensionless name.
	groupRe = regexp.MustCompile(`-\s?([A-Za-z0-9]+(?:\[[^\]]*\])?)$`)
	tagRe   = regexp.MustCompile(`\[[^\]]*\]$`)

	// Leading "[Group] " prefix (anime-style).
	bracketPrefixRe = regexp.MustCompile(`^\[([^\]]+)\]\s*`)

	// Unambiguous technical/edition tokens: these terminate a title wherever
	// they appear (no English sentence contains "HDR10" or "Directors Cut"
	// mid-title by accident).
	otherBoundaryRe = regexp.MustCompile(`(?i)(?:^|` + sep + `)(repack|extended|unrated|remastered|hdr10\+?|hdr|dolby[ ._]?vision|dovi|atmos|dd[p+]?[ ._]?[57][ ._]?1|dts(?:[ ._\-]?(?:hd|x|ma))?|truehd|aac(?:[ ._]?2[ ._]?0)?|ac3|eac3|flac|opus|10[ ._]?bit|8[ ._]?bit|imax|criterion|directors?[ ._]?cut|theatrical)(?:$|` + sep + `)`)

	// Ambiguous dictionary words ("The Complete History of…"): these only
	// count as boundaries when they appear after another anchor (year or a
	// technical token), otherwise they would truncate legitimate titles.
	ambiguousBoundaryRe = regexp.MustCompile(`(?i)(?:^|` + sep + `)(proper|restored|uncut|internal|limited|complete|multi|dubbed|subbed|hybrid|dv|3d)(?:$|` + sep + `)`)
)

type match struct {
	start int
	ok    bool
}

func earliest(ms ...match) match {
	best := match{ok: false}
	for _, m := range ms {
		if m.ok && (!best.ok || m.start < best.start) {
			best = m
		}
	}
	return best
}

func findFirst(re *regexp.Regexp, s string) match {
	if loc := re.FindStringIndex(s); loc != nil {
		return match{start: loc[0], ok: true}
	}
	return match{ok: false}
}

// Parse extracts release metadata from name. ok is false when the result is
// not confident enough to organize by (no title, or neither a year nor a TV
// marker); callers should then keep the raw name.
func Parse(name string) (Info, bool) {
	info := Info{Season: -1, Episode: -1, EpisodeEnd: -1}
	s := strings.TrimSpace(extRe.ReplaceAllString(strings.TrimSpace(name), ""))
	if s == "" {
		return info, false
	}

	// Anime-style "[Group] Title …" prefix.
	if m := bracketPrefixRe.FindStringSubmatch(s); m != nil {
		info.Group = strings.TrimSpace(m[1])
		s = s[len(m[0]):]
	}

	// TV markers (highest priority boundary).
	tvStart := match{ok: false}
	if m := sxeRe.FindStringSubmatchIndex(s); m != nil {
		info.IsTV = true
		info.Season = atoi(s[m[2]:m[3]])
		info.Episode = atoi(s[m[4]:m[5]])
		if m[6] >= 0 { // repeated group keeps its final iteration
			info.EpisodeEnd = atoi(s[m[6]:m[7]])
		}
		tvStart = match{start: m[0], ok: true}
	} else if m := nxnRe.FindStringSubmatchIndex(s); m != nil {
		info.IsTV = true
		info.Season = atoi(s[m[2]:m[3]])
		info.Episode = atoi(s[m[4]:m[5]])
		tvStart = match{start: m[0], ok: true}
	} else if m := seasonRe.FindStringSubmatchIndex(s); m != nil {
		info.IsTV = true
		info.Season = atoi(s[m[2]:m[3]])
		tvStart = match{start: m[0], ok: true}
	}

	resStart := findFirst(resolutionRe, s)
	srcStart := findFirst(sourceRe, s)
	codecStart := findFirst(codecRe, s)
	otherStart := findFirst(otherBoundaryRe, s)
	// Technical tokens never precede the year in a release name; edition
	// tokens ("Directors Cut") sometimes do, so they bound the title but not
	// the year search.
	strongBoundary := earliest(tvStart, resStart, srcStart, codecStart)
	firstNonYear := strongBoundary

	// Year: the last plausible year before the first non-year boundary (so
	// "2001 A Space Odyssey 1968 1080p" titles correctly), never at position
	// 0 (a leading year is part of the title, as in "1917" or "2012").
	// Candidates are found bare and boundary-checked by hand: adjacent years
	// ("2001.1968") share their separator, which anchored regexes cannot
	// re-match.
	yearStart := match{ok: false}
	maxYear := time.Now().Year() + 2
	for _, m := range yearCandRe.FindAllStringIndex(s, -1) {
		start, end := m[0], m[1]
		if !isTokenEdge(s, start-1) || !isTokenEdge(s, end) {
			continue
		}
		y := atoi(s[start:end])
		if y < 1900 || y > maxYear || start == 0 {
			continue
		}
		if firstNonYear.ok && start > firstNonYear.start {
			break
		}
		info.Year = y
		yearStart = match{start: start, ok: true}
	}

	// Ambiguous words become boundaries only past an existing anchor.
	anchor := earliest(strongBoundary, otherStart, yearStart)
	ambStart := match{ok: false}
	if anchor.ok {
		for _, m := range ambiguousBoundaryRe.FindAllStringIndex(s, -1) {
			if m[0] >= anchor.start {
				ambStart = match{start: m[0], ok: true}
				break
			}
		}
	}

	titleEnd := earliest(firstNonYear, otherStart, ambStart, yearStart)
	if !titleEnd.ok {
		return info, false // nothing recognized at all
	}
	info.Title = cleanTitle(s[:titleEnd.start])
	if info.Title == "" {
		return info, false
	}

	if resStart.ok {
		info.Resolution = normalizeResolution(resolutionRe.FindStringSubmatch(s)[1])
	}
	if srcStart.ok {
		info.Source = normalizeSource(sourceRe.FindStringSubmatch(s)[1])
	}
	if codecStart.ok {
		info.Codec = normalizeCodec(codecRe.FindStringSubmatch(s)[1])
	}
	// A trailing -GROUP only counts when it sits after the title (otherwise
	// hyphenated titles like "Spider-Man" would donate a bogus group) and
	// when the candidate is not the tail of a hyphenated known token
	// ("WEB-DL" must not yield group "DL").
	if info.Group == "" {
		if m := groupRe.FindStringSubmatchIndex(s); m != nil && m[2] > titleEnd.start {
			g := tagRe.ReplaceAllString(s[m[2]:m[3]], "")
			if !isKnownTokenTail(g) {
				info.Group = g
			}
		}
	}

	// Confidence: a year or a TV marker anchors the parse.
	if info.Year == 0 && !info.IsTV {
		return info, false
	}
	return info, true
}

var titleSepRe = strings.NewReplacer(".", " ", "_", " ", "(", " ", ")", " ", "[", " ", "]", " ", ",", " ")

// cleanTitle converts separator characters to spaces and tidies the result.
// Hyphens survive inside words ("Spider-Man") but are trimmed at the edges.
func cleanTitle(s string) string {
	s = titleSepRe.Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.Trim(s, "- ")
}

func normalizeResolution(s string) string {
	switch strings.ToLower(s) {
	case "4k", "uhd":
		return "2160p"
	default:
		return strings.ToLower(s)
	}
}

var normSep = strings.NewReplacer(" ", "", ".", "", "_", "", "-", "")

func normalizeSource(s string) string {
	c := strings.ToLower(normSep.Replace(s))
	switch c {
	case "bluray":
		return "BluRay"
	case "bdrip":
		return "BDRip"
	case "brrip":
		return "BRRip"
	case "webdl", "web":
		return "WEB-DL"
	case "webrip", "webmux":
		return "WEBRip"
	case "hdtv":
		return "HDTV"
	case "dvdrip", "dvd", "dvdscr":
		return "DVDRip"
	case "remux":
		return "REMUX"
	case "hdrip":
		return "HDRip"
	case "cam", "camrip", "hdcam", "telesync", "telecine", "screener", "scr":
		return "CAM"
	case "vhs":
		return "VHS"
	default:
		return s
	}
}

func normalizeCodec(s string) string {
	c := strings.ToLower(normSep.Replace(s))
	switch c {
	case "x264":
		return "x264"
	case "x265":
		return "x265"
	case "h264", "avc":
		return "H.264"
	case "h265", "hevc":
		return "HEVC"
	case "av1":
		return "AV1"
	case "xvid":
		return "XviD"
	case "divx":
		return "DivX"
	case "vp9":
		return "VP9"
	default:
		return s
	}
}

// isTokenEdge reports whether position i (which may be -1 or len(s)) is a
// token boundary: outside the string or a separator character. Byte-wise
// indexing is safe here because the separator set is pure ASCII — UTF-8
// continuation/lead bytes are all >= 0x80 and can never false-match.
func isTokenEdge(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return true
	}
	return strings.ContainsRune(" ._-[](),", rune(s[i]))
}

// groupStoplist are hyphen-tails of known tokens and other strings that must
// never be mistaken for a release group.
var groupStoplist = map[string]bool{
	"dl": true, "rip": true, "ray": true, "web": true, "hd": true,
	"ma": true, "x": true, "hdr": true, "dv": true, "cut": true,
}

func isKnownTokenTail(g string) bool {
	lg := strings.ToLower(g)
	if groupStoplist[lg] {
		return true
	}
	if strings.Trim(lg, "0123456789") == "" {
		return true // bare numbers ("...DDP5-1") are never groups
	}
	// Full-token matches of the field classes are not groups either.
	for _, re := range []*regexp.Regexp{resolutionRe, codecRe, sourceRe, otherBoundaryRe, ambiguousBoundaryRe} {
		if loc := re.FindStringIndex(lg); loc != nil && loc[0] == 0 && loc[1] == len(lg) {
			return true
		}
	}
	return false
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

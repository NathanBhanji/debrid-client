// Package organize renders library-style directory paths ("Movie Name
// (2019)", "Show/Season 02") from parsed release names and user templates.
//
// Templates are literal text with {variable} placeholders — deliberately not
// a scripting language. Variables: {title} {year} {season} {episode}
// {episode_end} {resolution} {source} {codec} {group}; numeric variables
// accept a zero-pad width as {season:02}. "/" in a template nests folders.
// Absent values render empty and the surrounding punctuation is tidied, so
// "{title} ({year})" without a year degrades to just the title.
package organize

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/NathanBhanji/debrid-client/internal/relname"
)

// Defaults follow Jellyfin/Plex library conventions.
const (
	DefaultMovieTemplate = "{title} ({year})"
	DefaultTVTemplate    = "{title} ({year})/Season {season:02}"
)

// Settings is the organize block of the runtime settings.
type Settings struct {
	// Enabled turns library organization on for new torrents.
	Enabled bool `json:"enabled"`
	// MovieTemplate lays out non-TV releases. Empty = default.
	MovieTemplate string `json:"movie_template,omitempty"`
	// TVTemplate lays out releases with a season/episode marker. Empty = default.
	TVTemplate string `json:"tv_template,omitempty"`
}

// Template returns the effective template for parsed info.
func (s Settings) Template(info relname.Info) string {
	if info.IsTV && info.Season >= 0 {
		if s.TVTemplate != "" {
			return s.TVTemplate
		}
		return DefaultTVTemplate
	}
	if s.MovieTemplate != "" {
		return s.MovieTemplate
	}
	return DefaultMovieTemplate
}

var varRe = regexp.MustCompile(`\{([a-z_]+)(?::(\d{1,2}))?\}`)

// knownVars maps variable names to extractors. Empty string = absent.
var knownVars = map[string]func(relname.Info) string{
	"title":   func(i relname.Info) string { return i.Title },
	"year":    func(i relname.Info) string { return numOrEmpty(i.Year, i.Year > 0) },
	"season":  func(i relname.Info) string { return numOrEmpty(i.Season, i.Season >= 0) },
	"episode": func(i relname.Info) string { return numOrEmpty(i.Episode, i.Episode >= 0) },
	"episode_end": func(i relname.Info) string {
		return numOrEmpty(i.EpisodeEnd, i.EpisodeEnd >= 0)
	},
	"resolution": func(i relname.Info) string { return i.Resolution },
	"source":     func(i relname.Info) string { return i.Source },
	"codec":      func(i relname.Info) string { return i.Codec },
	"group":      func(i relname.Info) string { return i.Group },
}

func numOrEmpty(n int, present bool) string {
	if !present {
		return ""
	}
	return strconv.Itoa(n)
}

// ValidateTemplate rejects unknown variables and templates that could escape
// or produce nothing. Called when settings are saved.
func ValidateTemplate(tpl string) error {
	if strings.TrimSpace(tpl) == "" {
		return fmt.Errorf("template is empty")
	}
	if strings.Contains(tpl, "\\") {
		return fmt.Errorf(`template must use "/" separators`)
	}
	rest := varRe.ReplaceAllString(tpl, "")
	if strings.Contains(rest, "{") || strings.Contains(rest, "}") {
		return fmt.Errorf("malformed {variable} placeholder")
	}
	for _, m := range varRe.FindAllStringSubmatch(tpl, -1) {
		if _, ok := knownVars[m[1]]; !ok {
			return fmt.Errorf("unknown variable {%s}", m[1])
		}
	}
	for _, seg := range strings.Split(tpl, "/") {
		clean := strings.Trim(strings.TrimSpace(varRe.ReplaceAllString(seg, "x")), ". ")
		if clean == "" || clean == ".." {
			return fmt.Errorf("template segment %q is empty or escapes", seg)
		}
	}
	if !strings.Contains(tpl, "{title}") {
		return fmt.Errorf("template must contain {title}")
	}
	return nil
}

// Render fills tpl from info and returns cleaned "/"-separated segments.
// A segment containing variables that all render empty is dropped entirely
// ("Season {season:02}" without a season vanishes rather than leaving a bare
// "Season" folder); ok is false when nothing usable remains.
func Render(tpl string, info relname.Info) (segments []string, ok bool) {
	for _, segTpl := range strings.Split(tpl, "/") {
		vars, filled := 0, 0
		rendered := varRe.ReplaceAllStringFunc(segTpl, func(tok string) string {
			m := varRe.FindStringSubmatch(tok)
			vars++
			v := knownVars[m[1]](info)
			if v == "" {
				return ""
			}
			filled++
			if m[2] != "" {
				if n, err := strconv.Atoi(v); err == nil {
					width, _ := strconv.Atoi(m[2])
					return fmt.Sprintf("%0*d", width, n)
				}
			}
			return v
		})
		if vars > 0 && filled == 0 {
			continue
		}
		if s := tidySegment(rendered); s != "" {
			segments = append(segments, s)
		}
	}
	return segments, len(segments) > 0
}

var (
	emptyBracketsRe = regexp.MustCompile(`\(\s*\)|\[\s*\]`)
	spaceRunRe      = regexp.MustCompile(`\s{2,}`)
)

// tidySegment cleans a rendered path segment: empty punctuation groups left
// behind by absent variables are removed, whitespace is collapsed, and
// dangling separators are trimmed.
func tidySegment(seg string) string {
	seg = emptyBracketsRe.ReplaceAllString(seg, "")
	seg = spaceRunRe.ReplaceAllString(seg, " ")
	return strings.Trim(seg, " -._")
}

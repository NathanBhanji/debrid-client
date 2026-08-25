package organize

import (
	"strings"
	"testing"

	"github.com/NathanBhanji/debrid-client/internal/relname"
)

func TestRender(t *testing.T) {
	movie := relname.Info{Title: "Some Movie", Year: 2019, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "2160p", Source: "WEB-DL", Codec: "x265", Group: "GRP"}
	tvPack := relname.Info{Title: "Some Show", Year: 0, Season: 2, Episode: -1, EpisodeEnd: -1, IsTV: true}
	tvDated := relname.Info{Title: "Doctor Who", Year: 2005, Season: 1, Episode: 1, EpisodeEnd: -1, IsTV: true}
	noYear := relname.Info{Title: "Show", Year: 0, Season: -1, Episode: -1, EpisodeEnd: -1}

	cases := []struct {
		tpl  string
		info relname.Info
		want string
	}{
		{DefaultMovieTemplate, movie, "Some Movie (2019)"},
		{DefaultTVTemplate, tvPack, "Some Show/Season 02"},
		{DefaultTVTemplate, tvDated, "Doctor Who (2005)/Season 01"},
		// Absent year degrades cleanly; absent season drops its segment.
		{DefaultMovieTemplate, noYear, "Show"},
		{"{title} ({year})/Season {season:02}", noYear, "Show"},
		{"{title} [{resolution}] [{codec}]", movie, "Some Movie [2160p] [x265]"},
		{"{title} [{resolution}]", tvPack, "Some Show"},
		{"{title}/{year} {source}-{group}", movie, "Some Movie/2019 WEB-DL-GRP"},
		{"{title} - {episode:03}", tvDated, "Doctor Who - 001"},
	}
	for _, tc := range cases {
		segs, ok := Render(tc.tpl, tc.info)
		if !ok {
			t.Errorf("Render(%q, %s): not ok", tc.tpl, tc.info.Title)
			continue
		}
		if got := strings.Join(segs, "/"); got != tc.want {
			t.Errorf("Render(%q, %s) = %q, want %q", tc.tpl, tc.info.Title, got, tc.want)
		}
	}

	// A template whose every segment collapses reports !ok.
	if segs, ok := Render("{group}/{resolution}", relname.Info{Title: "X", Season: -1, Episode: -1, EpisodeEnd: -1}); ok {
		t.Errorf("all-empty render ok: %v", segs)
	}
}

func TestTemplateSelection(t *testing.T) {
	s := Settings{}
	tv := relname.Info{IsTV: true, Season: 1}
	if got := s.Template(tv); got != DefaultTVTemplate {
		t.Errorf("tv template = %q", got)
	}
	if got := s.Template(relname.Info{Season: -1}); got != DefaultMovieTemplate {
		t.Errorf("movie template = %q", got)
	}
	s = Settings{MovieTemplate: "m", TVTemplate: "t"}
	if s.Template(tv) != "t" || s.Template(relname.Info{Season: -1}) != "m" {
		t.Error("custom templates not selected")
	}
}

func TestValidateTemplate(t *testing.T) {
	for _, ok := range []string{
		DefaultMovieTemplate,
		DefaultTVTemplate,
		"{title} [{resolution}] {source} {codec} {group} {episode_end}",
		"Library/{title} ({year})",
	} {
		if err := ValidateTemplate(ok); err != nil {
			t.Errorf("ValidateTemplate(%q) = %v", ok, err)
		}
	}
	for tpl, why := range map[string]string{
		"":                         "empty",
		"   ":                      "empty",
		"{title} {bogus}":          "unknown variable",
		"{title} {unclosed":        "malformed",
		"{year}":                   "missing {title}",
		"{title}/../{year}":        "escape",
		"{title}\\Season {season}": "backslash",
		"{title}//x":               "empty segment",
	} {
		if err := ValidateTemplate(tpl); err == nil {
			t.Errorf("ValidateTemplate(%q): expected error (%s)", tpl, why)
		}
	}
}

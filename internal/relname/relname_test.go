package relname

import (
	"strings"
	"testing"
	"time"
)

func TestParseCorpus(t *testing.T) {
	cases := []struct {
		name string
		want Info
	}{
		// --- movies -----------------------------------------------------
		{"Some.Movie.2019.2160p.WEB-DL.DV.HDR.x265-GROUP.mkv",
			Info{Title: "Some Movie", Year: 2019, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "2160p", Source: "WEB-DL", Codec: "x265", Group: "GROUP"}},
		{"Inception.2010.1080p.BluRay.x264-SPARKS",
			Info{Title: "Inception", Year: 2010, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "BluRay", Codec: "x264", Group: "SPARKS"}},
		{"Inception (2010) 1080p BluRay x264-SPARKS",
			Info{Title: "Inception", Year: 2010, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "BluRay", Codec: "x264", Group: "SPARKS"}},
		{"1917.2019.1080p.BluRay.x264-AMIABLE",
			Info{Title: "1917", Year: 2019, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "BluRay", Codec: "x264", Group: "AMIABLE"}},
		{"2001.A.Space.Odyssey.1968.2160p.UHD.BluRay.REMUX.HDR.HEVC-FGT",
			Info{Title: "2001 A Space Odyssey", Year: 1968, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "2160p", Source: "BluRay", Codec: "HEVC", Group: "FGT"}},
		{"2012.2009.1080p.BluRay.x264",
			Info{Title: "2012", Year: 2009, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "BluRay", Codec: "x264"}},
		{"Spider-Man.No.Way.Home.2021.1080p.WEBRip.x265-RARBG",
			Info{Title: "Spider-Man No Way Home", Year: 2021, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "WEBRip", Codec: "x265", Group: "RARBG"}},
		{"The.Matrix.1999.REMASTERED.1080p.BluRay.H264.AAC-RARBG",
			Info{Title: "The Matrix", Year: 1999, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "BluRay", Codec: "H.264", Group: "RARBG"}},
		{"some.movie.2019.720p.hdtv.x264-group",
			Info{Title: "some movie", Year: 2019, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "720p", Source: "HDTV", Codec: "x264", Group: "group"}},
		{"Movie.Name.2024.HDR.2160p.WEB.h265-GRP",
			Info{Title: "Movie Name", Year: 2024, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "2160p", Source: "WEB-DL", Codec: "HEVC", Group: "GRP"}},
		// No group after a hyphenated source: "-DL" must not become a group.
		{"Some.Movie.2019.1080p.WEB-DL",
			Info{Title: "Some Movie", Year: 2019, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "WEB-DL"}},
		{"Old.Film.1954.DVDRip.XviD",
			Info{Title: "Old Film", Year: 1954, Season: -1, Episode: -1, EpisodeEnd: -1, Source: "DVDRip", Codec: "XviD"}},
		{"Movie.2023.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR10+.x265-FLUX",
			Info{Title: "Movie", Year: 2023, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "2160p", Source: "WEB-DL", Codec: "x265", Group: "FLUX"}},
		{"Movie.Title.2020.4K.HDR.WEBRip.AV1",
			Info{Title: "Movie Title", Year: 2020, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "2160p", Source: "WEBRip", Codec: "AV1"}},
		{"Film_With_Underscores_2018_720p_BRRip",
			Info{Title: "Film With Underscores", Year: 2018, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "720p", Source: "BRRip"}},
		{"Movie.Directors.Cut.2001.576p.DVDRip",
			Info{Title: "Movie", Year: 2001, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "576p", Source: "DVDRip"}},
		// Year only, nothing else — still enough to organize.
		{"Tiny.Indie.Film.2015",
			Info{Title: "Tiny Indie Film", Year: 2015, Season: -1, Episode: -1, EpisodeEnd: -1}},

		// --- tv ---------------------------------------------------------
		{"Some.Show.S02E05.1080p.WEB-DL.x265-GRP",
			Info{Title: "Some Show", Year: 0, Season: 2, Episode: 5, EpisodeEnd: -1, Resolution: "1080p", Source: "WEB-DL", Codec: "x265", Group: "GRP", IsTV: true}},
		{"Some.Show.S02.COMPLETE.1080p.BluRay.x265-GRP",
			Info{Title: "Some Show", Year: 0, Season: 2, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "BluRay", Codec: "x265", Group: "GRP", IsTV: true}},
		{"Show.2019.S01E01.720p.WEB-DL",
			Info{Title: "Show", Year: 2019, Season: 1, Episode: 1, EpisodeEnd: -1, Resolution: "720p", Source: "WEB-DL", IsTV: true}},
		{"The.Office.US.1x05.HDTV.XviD-LOL",
			Info{Title: "The Office US", Year: 0, Season: 1, Episode: 5, EpisodeEnd: -1, Source: "HDTV", Codec: "XviD", Group: "LOL", IsTV: true}},
		{"Show.Name.S01E01E02.720p.HDTV",
			Info{Title: "Show Name", Year: 0, Season: 1, Episode: 1, EpisodeEnd: 2, Resolution: "720p", Source: "HDTV", IsTV: true}},
		{"Breaking.Bad.S05E14.Ozymandias.1080p.BluRay.x264",
			Info{Title: "Breaking Bad", Year: 0, Season: 5, Episode: 14, EpisodeEnd: -1, Resolution: "1080p", Source: "BluRay", Codec: "x264", IsTV: true}},
		{"Blade.Runner.2049.2017.2160p.WEB-DL.x265-GRP",
			Info{Title: "Blade Runner 2049", Year: 2017, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "2160p", Source: "WEB-DL", Codec: "x265", Group: "GRP"}},
		{"Wonder.Woman.1984.2020.1080p.WEB-DL",
			Info{Title: "Wonder Woman 1984", Year: 2020, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "WEB-DL"}},
		{"It.2017.1080p.BluRay.x264",
			Info{Title: "It", Year: 2017, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "BluRay", Codec: "x264"}},
		{"2046.2004.1080p.BluRay",
			Info{Title: "2046", Year: 2004, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "BluRay"}},
		{"The.Amazing.Spider-Man.2.2014.1080p.WEB-DL",
			Info{Title: "The Amazing Spider-Man 2", Year: 2014, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "WEB-DL"}},
		{"Amélie.2001.1080p.BluRay",
			Info{Title: "Amélie", Year: 2001, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "BluRay"}},
		// Ambiguous dictionary words stay in the title before the year…
		{"The.Complete.History.of.The.Beatles.2010.1080p.BluRay",
			Info{Title: "The Complete History of The Beatles", Year: 2010, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "BluRay"}},
		{"Uncut.Gems.2019.1080p.WEB-DL",
			Info{Title: "Uncut Gems", Year: 2019, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "WEB-DL"}},
		// …but bound it after an anchor.
		{"Movie.2019.UNCUT.1080p.BluRay",
			Info{Title: "Movie", Year: 2019, Season: -1, Episode: -1, EpisodeEnd: -1, Resolution: "1080p", Source: "BluRay"}},
		{"Doctor.Who.2005.S01E01.720p.HDTV",
			Info{Title: "Doctor Who", Year: 2005, Season: 1, Episode: 1, EpisodeEnd: -1, Resolution: "720p", Source: "HDTV", IsTV: true}},
		// A year inside an episode title is past the TV boundary: not taken.
		{"Show.S01E05.The.1984.Problem.720p.HDTV",
			Info{Title: "Show", Year: 0, Season: 1, Episode: 5, EpisodeEnd: -1, Resolution: "720p", Source: "HDTV", IsTV: true}},
		{"Show.S01E01.720p.HDTV.x264-KILLERS[eztv]",
			Info{Title: "Show", Year: 0, Season: 1, Episode: 1, EpisodeEnd: -1, Resolution: "720p", Source: "HDTV", Codec: "x264", Group: "KILLERS", IsTV: true}},
		{"show.s10e03.web.h264-grp",
			Info{Title: "show", Year: 0, Season: 10, Episode: 3, EpisodeEnd: -1, Source: "WEB-DL", Codec: "H.264", Group: "grp", IsTV: true}},
	}
	for _, tc := range cases {
		got, ok := Parse(tc.name)
		if !ok {
			t.Errorf("%q: not parsed (got %+v)", tc.name, got)
			continue
		}
		if got != tc.want {
			t.Errorf("%q:\n got  %+v\n want %+v", tc.name, got, tc.want)
		}
	}
}

// Names that must NOT be organized: parsing reports ok=false and callers
// keep the raw name. Wrongly organizing is worse than not organizing.
func TestParseRejects(t *testing.T) {
	for _, name := range []string{
		"",
		"Random Data",
		"ubuntu-24.04-desktop-amd64.iso",
		"My Photos Backup",
		"Movie.Without.Anything",
		"1080p.WEB-DL.x265", // boundary at position 0: no title
		"Cam.2018.1080p",    // title collides with a source token: bail out
		"S01E02.720p",       // TV marker but no title
		// Anime absolute numbering has no year/SxxEyy anchor (v1 limitation).
		"[SubsPlease] Some Anime - 05 (1080p) [ABC123].mkv",
		// No release year: 2049 is over the max-year guard, so nothing
		// anchors the parse. Falling back beats titling it "Blade Runner".
		"Blade.Runner.2049.2160p.WEB-DL.x265-GRP",
		// A bare "Web" title token is a source boundary before the year:
		// unparsed by design rather than titled "Charlottes".
		"Charlottes.Web.2006.1080p.BluRay",
	} {
		if info, ok := Parse(name); ok {
			t.Errorf("%q: unexpectedly parsed to %+v", name, info)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add("Some.Movie.2019.2160p.WEB-DL.x265-GROUP.mkv")
	f.Add("Show.S01E02.720p")
	f.Add("[G] T - 01 (1080p)")
	f.Add("Amélie.2001.1080p.BluRay")
	f.Add("Show.S01E01.720p.HDTV.x264-KILLERS[eztv]")
	f.Fuzz(func(t *testing.T, name string) {
		// Must never panic; parsed results obey the struct invariants.
		info, ok := Parse(name)
		if !ok {
			return
		}
		if info.Title == "" {
			t.Fatalf("parsed with empty title: %+v", info)
		}
		if strings.ContainsAny(info.Title, "._[]()") {
			t.Fatalf("separator leaked into title: %+v", info)
		}
		if info.Year != 0 && (info.Year < 1900 || info.Year > time.Now().Year()+2) {
			t.Fatalf("implausible year: %+v", info)
		}
		if !info.IsTV && (info.Season != -1 || info.Episode != -1) {
			t.Fatalf("season/episode without IsTV: %+v", info)
		}
		if info.Season > 99 || info.Episode > 999 || info.EpisodeEnd > 999 {
			t.Fatalf("out-of-range season/episode: %+v", info)
		}
	})
}

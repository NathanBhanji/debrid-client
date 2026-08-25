package relname

import "testing"

func TestParseCorpus(t *testing.T) {
	cases := []struct {
		name string
		want Info
	}{
		// --- movies -----------------------------------------------------
		{"Some.Movie.2019.2160p.WEB-DL.DV.HDR.x265-GROUP.mkv",
			Info{Title: "Some Movie", Year: 2019, Season: -1, Episode: -1, Resolution: "2160p", Source: "WEB-DL", Codec: "x265", Group: "GROUP"}},
		{"Inception.2010.1080p.BluRay.x264-SPARKS",
			Info{Title: "Inception", Year: 2010, Season: -1, Episode: -1, Resolution: "1080p", Source: "BluRay", Codec: "x264", Group: "SPARKS"}},
		{"Inception (2010) 1080p BluRay x264-SPARKS",
			Info{Title: "Inception", Year: 2010, Season: -1, Episode: -1, Resolution: "1080p", Source: "BluRay", Codec: "x264", Group: "SPARKS"}},
		{"1917.2019.1080p.BluRay.x264-AMIABLE",
			Info{Title: "1917", Year: 2019, Season: -1, Episode: -1, Resolution: "1080p", Source: "BluRay", Codec: "x264", Group: "AMIABLE"}},
		{"2001.A.Space.Odyssey.1968.2160p.UHD.BluRay.REMUX.HDR.HEVC-FGT",
			Info{Title: "2001 A Space Odyssey", Year: 1968, Season: -1, Episode: -1, Resolution: "2160p", Source: "BluRay", Codec: "HEVC", Group: "FGT"}},
		{"2012.2009.1080p.BluRay.x264",
			Info{Title: "2012", Year: 2009, Season: -1, Episode: -1, Resolution: "1080p", Source: "BluRay", Codec: "x264"}},
		{"Spider-Man.No.Way.Home.2021.1080p.WEBRip.x265-RARBG",
			Info{Title: "Spider-Man No Way Home", Year: 2021, Season: -1, Episode: -1, Resolution: "1080p", Source: "WEBRip", Codec: "x265", Group: "RARBG"}},
		{"The.Matrix.1999.REMASTERED.1080p.BluRay.H264.AAC-RARBG",
			Info{Title: "The Matrix", Year: 1999, Season: -1, Episode: -1, Resolution: "1080p", Source: "BluRay", Codec: "H.264", Group: "RARBG"}},
		{"some.movie.2019.720p.hdtv.x264-group",
			Info{Title: "some movie", Year: 2019, Season: -1, Episode: -1, Resolution: "720p", Source: "HDTV", Codec: "x264", Group: "group"}},
		{"Movie.Name.2024.HDR.2160p.WEB.h265-GRP",
			Info{Title: "Movie Name", Year: 2024, Season: -1, Episode: -1, Resolution: "2160p", Source: "WEB-DL", Codec: "HEVC", Group: "GRP"}},
		// No group after a hyphenated source: "-DL" must not become a group.
		{"Some.Movie.2019.1080p.WEB-DL",
			Info{Title: "Some Movie", Year: 2019, Season: -1, Episode: -1, Resolution: "1080p", Source: "WEB-DL"}},
		{"Old.Film.1954.DVDRip.XviD",
			Info{Title: "Old Film", Year: 1954, Season: -1, Episode: -1, Source: "DVDRip", Codec: "XviD"}},
		{"Movie.2023.2160p.WEB-DL.DDP5.1.Atmos.DV.HDR10+.x265-FLUX",
			Info{Title: "Movie", Year: 2023, Season: -1, Episode: -1, Resolution: "2160p", Source: "WEB-DL", Codec: "x265", Group: "FLUX"}},
		{"Movie.Title.2020.4K.HDR.WEBRip.AV1",
			Info{Title: "Movie Title", Year: 2020, Season: -1, Episode: -1, Resolution: "2160p", Source: "WEBRip", Codec: "AV1"}},
		{"Film_With_Underscores_2018_720p_BRRip",
			Info{Title: "Film With Underscores", Year: 2018, Season: -1, Episode: -1, Resolution: "720p", Source: "BRRip"}},
		{"Movie.Directors.Cut.2001.576p.DVDRip",
			Info{Title: "Movie", Year: 2001, Season: -1, Episode: -1, Resolution: "576p", Source: "DVDRip"}},
		// Year only, nothing else — still enough to organize.
		{"Tiny.Indie.Film.2015",
			Info{Title: "Tiny Indie Film", Year: 2015, Season: -1, Episode: -1}},

		// --- tv ---------------------------------------------------------
		{"Some.Show.S02E05.1080p.WEB-DL.x265-GRP",
			Info{Title: "Some Show", Year: 0, Season: 2, Episode: 5, Resolution: "1080p", Source: "WEB-DL", Codec: "x265", Group: "GRP", IsTV: true}},
		{"Some.Show.S02.COMPLETE.1080p.BluRay.x265-GRP",
			Info{Title: "Some Show", Year: 0, Season: 2, Episode: -1, Resolution: "1080p", Source: "BluRay", Codec: "x265", Group: "GRP", IsTV: true}},
		{"Show.2019.S01E01.720p.WEB-DL",
			Info{Title: "Show", Year: 2019, Season: 1, Episode: 1, Resolution: "720p", Source: "WEB-DL", IsTV: true}},
		{"The.Office.US.1x05.HDTV.XviD-LOL",
			Info{Title: "The Office US", Year: 0, Season: 1, Episode: 5, Source: "HDTV", Codec: "XviD", Group: "LOL", IsTV: true}},
		{"Show.Name.S01E01E02.720p.HDTV",
			Info{Title: "Show Name", Year: 0, Season: 1, Episode: 1, Resolution: "720p", Source: "HDTV", IsTV: true}},
		{"Breaking.Bad.S05E14.Ozymandias.1080p.BluRay.x264",
			Info{Title: "Breaking Bad", Year: 0, Season: 5, Episode: 14, Resolution: "1080p", Source: "BluRay", Codec: "x264", IsTV: true}},
		{"show.s10e03.web.h264-grp",
			Info{Title: "show", Year: 0, Season: 10, Episode: 3, Source: "WEB-DL", Codec: "H.264", Group: "grp", IsTV: true}},
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
	f.Fuzz(func(t *testing.T, name string) {
		// Must never panic; season/episode stay in sane ranges.
		info, ok := Parse(name)
		if ok && info.Title == "" {
			t.Fatalf("parsed with empty title: %+v", info)
		}
		if info.Season > 99 || info.Episode > 999 {
			t.Fatalf("out-of-range season/episode: %+v", info)
		}
	})
}

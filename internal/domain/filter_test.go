package domain

import (
	"errors"
	"testing"
)

var sample = []File{
	{ID: "1", Path: "Show/Show.S01E01.mkv", Size: 1_500_000_000},
	{ID: "2", Path: "Show/Show.S01E02.mkv", Size: 1_400_000_000},
	{ID: "3", Path: "Show/Sample/sample.mkv", Size: 50_000_000},
	{ID: "4", Path: "Show/Show.nfo", Size: 2_000},
	{ID: "5", Path: "/Show/Subs/Show.S01E01.srt", Size: 60_000},
	{ID: "6", Path: "zero-size-unknown.mkv", Size: 0},
}

func ids(fs []File) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.ID
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSelectFiles(t *testing.T) {
	cases := []struct {
		name    string
		s       TorrentSettings
		want    []string
		wantErr error
	}{
		{"no filters keeps all", TorrentSettings{}, []string{"1", "2", "3", "4", "5", "6"}, nil},
		{"min size", TorrentSettings{MinFileSize: 100_000_000}, []string{"1", "2", "6"}, nil},
		{"exclude regex (case-insensitive)", TorrentSettings{ExcludeRegex: `SAMPLE|\.nfo$|zero`}, []string{"1", "2", "5"}, nil},
		{"include regex wins over exclude", TorrentSettings{IncludeRegex: `S01E0\d\.mkv$|sample`, ExcludeRegex: `sample`}, []string{"1", "2", "3"}, nil},
		{"include + min size", TorrentSettings{IncludeRegex: `S01E0\d\.mkv$`, MinFileSize: 100_000_000}, []string{"1", "2"}, nil},
		{"leading slash normalised", TorrentSettings{IncludeRegex: `^Show/Subs/`}, []string{"5"}, nil},
		{"manual wins over everything", TorrentSettings{ManualFiles: []string{"4", "3"}, IncludeRegex: `\.mkv$`, MinFileSize: 1 << 40}, []string{"3", "4"}, nil},
		{"manual none match", TorrentSettings{ManualFiles: []string{"99"}}, nil, ErrNoFilesSelected},
		{"all excluded", TorrentSettings{MinFileSize: 1 << 40, ExcludeRegex: `zero`}, nil, ErrNoFilesSelected},
		{"bad regex", TorrentSettings{IncludeRegex: `(`}, nil, nil},
		{"unknown size kept by min size", TorrentSettings{MinFileSize: 100, IncludeRegex: `^zero`}, []string{"6"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := SelectFiles(sample, c.s)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				return
			}
			if c.name == "bad regex" {
				if err == nil {
					t.Fatal("expected regex compile error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if !eq(ids(got), c.want) {
				t.Fatalf("got %v want %v", ids(got), c.want)
			}
		})
	}
}

func TestValidateFilters(t *testing.T) {
	if err := ValidateFilters(TorrentSettings{IncludeRegex: `[`}); err == nil {
		t.Fatal("expected error for bad include regex")
	}
	if err := ValidateFilters(TorrentSettings{ExcludeRegex: `[`}); err == nil {
		t.Fatal("expected error for bad exclude regex")
	}
	if err := ValidateFilters(TorrentSettings{MinFileSize: -1}); err == nil {
		t.Fatal("expected error for negative min size")
	}
	if err := ValidateFilters(DefaultTorrentSettings()); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
}

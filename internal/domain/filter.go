package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrNoFilesSelected is returned when filters exclude every file.
var ErrNoFilesSelected = errors.New("no files selected")

// SelectFiles applies the torrent settings to the file list and returns the
// files to download. Rules, in order:
//   - ManualFiles, when set, wins outright (by provider file ID).
//   - IncludeRegex, when set, keeps only matching paths and ignores ExcludeRegex.
//   - Otherwise ExcludeRegex drops matching paths.
//   - MinFileSize then drops small files (files of unknown size, 0, are kept).
//
// Regexes are matched case-insensitively against the "/"-joined path.
// Returns ErrNoFilesSelected if nothing survives.
func SelectFiles(files []File, s TorrentSettings) ([]File, error) {
	if len(s.ManualFiles) > 0 {
		want := make(map[string]bool, len(s.ManualFiles))
		for _, id := range s.ManualFiles {
			want[id] = true
		}
		var out []File
		for _, f := range files {
			if want[f.ID] {
				out = append(out, f)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("%w: none of the manual file ids matched", ErrNoFilesSelected)
		}
		return out, nil
	}

	var include, exclude *regexp.Regexp
	var err error
	if s.IncludeRegex != "" {
		if include, err = compileCI(s.IncludeRegex); err != nil {
			return nil, fmt.Errorf("include_regex: %w", err)
		}
	} else if s.ExcludeRegex != "" {
		if exclude, err = compileCI(s.ExcludeRegex); err != nil {
			return nil, fmt.Errorf("exclude_regex: %w", err)
		}
	}

	var out []File
	for _, f := range files {
		p := strings.TrimPrefix(f.Path, "/")
		if include != nil && !include.MatchString(p) {
			continue
		}
		if exclude != nil && exclude.MatchString(p) {
			continue
		}
		if s.MinFileSize > 0 && f.Size > 0 && f.Size < s.MinFileSize {
			continue
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: filters excluded all %d files", ErrNoFilesSelected, len(files))
	}
	return out, nil
}

// ValidateFilters checks that the regexes in s compile.
func ValidateFilters(s TorrentSettings) error {
	var errs []error
	if s.IncludeRegex != "" {
		if _, err := compileCI(s.IncludeRegex); err != nil {
			errs = append(errs, fmt.Errorf("include_regex: %w", err))
		}
	}
	if s.ExcludeRegex != "" {
		if _, err := compileCI(s.ExcludeRegex); err != nil {
			errs = append(errs, fmt.Errorf("exclude_regex: %w", err))
		}
	}
	if s.MinFileSize < 0 {
		errs = append(errs, errors.New("min_file_size must be >= 0"))
	}
	return errors.Join(errs...)
}

func compileCI(expr string) (*regexp.Regexp, error) {
	return regexp.Compile("(?i)" + expr)
}

package api

import (
	"context"
	"strings"
)

// formatLocale turns the POSIX locale used for dates (LC_ALL, LC_TIME, LANG
// — the first one set) into a BCP 47 tag: "ru_RU.UTF-8" → "ru-RU". "" for
// C/POSIX/unset: the UI then formats with its own language.
func formatLocale(getenv func(string) string) string {
	var loc string
	for _, k := range []string{"LC_ALL", "LC_TIME", "LANG"} {
		if loc = getenv(k); loc != "" {
			break
		}
	}
	loc, _, _ = strings.Cut(loc, ".")
	loc, _, _ = strings.Cut(loc, "@")
	if loc == "" || loc == "C" || loc == "POSIX" {
		return ""
	}
	return strings.ReplaceAll(loc, "_", "-")
}

func (s *Service) AppInfo(context.Context) (AppInfo, error) {
	return AppInfo{FormatLocale: formatLocale(s.getenv)}, nil
}

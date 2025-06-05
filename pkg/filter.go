package eget2

import (
	"regexp"
	"strings"

	"github.com/gobwas/glob"
)

type FilterOptions struct {
	Regexes         []string
	Globs           []string
	MatchKeywords   []string
	ExcludeKeywords []string
	ExactCase       bool
	Tag             string
}

func matchesPatterns(name string, opts FilterOptions) bool {
	compareName := name
	if !opts.ExactCase {
		compareName = strings.ToLower(name)
	}

	for _, pattern := range opts.Regexes {
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		if !re.MatchString(name) {
			return false
		}
	}

	for _, pattern := range opts.Globs {
		g, err := glob.Compile(pattern)
		if err != nil {
			continue
		}
		if !g.Match(name) {
			return false
		}
	}

	for _, keyword := range opts.MatchKeywords {
		kw := keyword
		if !opts.ExactCase {
			kw = strings.ToLower(keyword)
		}
		for _, part := range strings.Split(kw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if !strings.Contains(compareName, part) {
				return false
			}
		}
	}

	for _, keyword := range opts.ExcludeKeywords {
		kw := keyword
		if !opts.ExactCase {
			kw = strings.ToLower(keyword)
		}
		for _, part := range strings.Split(kw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if strings.Contains(compareName, part) {
				return false
			}
		}
	}

	return true
}

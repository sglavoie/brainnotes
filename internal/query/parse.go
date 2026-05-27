package query

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Flags is the flattened set of values produced by the `find` Cobra flags. It
// is consumed by FromFlags to build a single predicate.
//
// The int fields MinBacklinks, MaxBacklinks, MinLinks, and MaxLinks use -1 as
// the sentinel for "not set". A value of 0 IS a meaningful filter (e.g.
// MaxBacklinks=0 matches notes with no inbound links). Values below -1 are
// invalid. Callers that construct Flags directly must initialise these fields
// to -1 to avoid unintended filtering — the Go zero value (0) would activate
// MaxBacklinks/MaxLinks.
type Flags struct {
	Tags         []string
	Categories   []string
	Status       string
	Type         string
	Has          string
	Missing      string
	Age          string
	Created      string
	Folder       string
	Orphan       bool
	BrokenLinks  bool
	MinBacklinks int
	MaxBacklinks int
	MinLinks     int
	MaxLinks     int
	DeadEnd      bool

	// Root is the absolute vault root; required when Folder is set.
	Root string
	// Graph must be populated by the caller when Orphan, BrokenLinks, or
	// link-count predicates are active.
	Graph *Graph
}

// FromFlags translates a Flags value into a single combined predicate. Empty
// strings and nil slices contribute nothing; the result is the And of every
// active term (matching every note when no terms are active).
func FromFlags(f Flags) (Predicate, error) {
	for _, spec := range []struct {
		flag  string
		value int
	}{
		{flag: "--min-backlinks", value: f.MinBacklinks},
		{flag: "--max-backlinks", value: f.MaxBacklinks},
		{flag: "--min-links", value: f.MinLinks},
		{flag: "--max-links", value: f.MaxLinks},
	} {
		if spec.value < -1 {
			return nil, fmt.Errorf("query: %s must be >= 0 (or -1 to disable), got %d", spec.flag, spec.value)
		}
	}
	for _, spec := range []struct {
		minFlag string
		min     int
		maxFlag string
		max     int
	}{
		{minFlag: "--min-backlinks", min: f.MinBacklinks, maxFlag: "--max-backlinks", max: f.MaxBacklinks},
		{minFlag: "--min-links", min: f.MinLinks, maxFlag: "--max-links", max: f.MaxLinks},
	} {
		if spec.min >= 0 && spec.max >= 0 && spec.min > spec.max {
			return nil, fmt.Errorf("query: %s (%d) cannot exceed %s (%d)", spec.minFlag, spec.min, spec.maxFlag, spec.max)
		}
	}

	var ps []Predicate
	for _, spec := range []struct {
		values []string
		build  func(string) Predicate
	}{
		{values: f.Tags, build: Tag},
		{values: f.Categories, build: Category},
	} {
		for _, value := range spec.values {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			ps = append(ps, spec.build(value))
		}
	}

	for _, spec := range []struct {
		value string
		build func(string) Predicate
	}{
		{value: f.Status, build: Status},
		{value: f.Type, build: Type},
		{value: f.Has, build: Has},
		{value: f.Missing, build: Missing},
	} {
		spec.value = strings.TrimSpace(spec.value)
		if spec.value == "" {
			continue
		}
		ps = append(ps, spec.build(spec.value))
	}

	for _, spec := range []struct {
		flag  string
		value string
		field string
	}{
		{flag: "--age", value: f.Age, field: "last"},
		{flag: "--created", value: f.Created, field: "created"},
	} {
		spec.value = strings.TrimSpace(spec.value)
		if spec.value == "" {
			continue
		}
		op, dur, err := ParseAge(spec.value)
		if err != nil {
			return nil, fmt.Errorf("query: %s: %w", spec.flag, err)
		}
		ps = append(ps, Age(op, dur, spec.field))
	}

	folder := strings.TrimSpace(f.Folder)
	if folder != "" {
		if f.Root == "" {
			return nil, errors.New("query: --folder requires a vault root path")
		}
		ps = append(ps, Folder(folder, f.Root))
	}

	for _, spec := range []struct {
		active bool
		flag   string
		build  func(*Graph) Predicate
	}{
		{active: f.Orphan, flag: "--orphan", build: Orphan},
		{active: f.BrokenLinks, flag: "--broken-links", build: BrokenLinks},
		{
			active: f.MinBacklinks > 0,
			flag:   "--min-backlinks",
			build: func(g *Graph) Predicate {
				return MinBacklinks(g, f.MinBacklinks)
			},
		},
		{
			active: f.MaxBacklinks >= 0,
			flag:   "--max-backlinks",
			build: func(g *Graph) Predicate {
				return MaxBacklinks(g, f.MaxBacklinks)
			},
		},
		{
			active: f.MinLinks > 0,
			flag:   "--min-links",
			build: func(g *Graph) Predicate {
				return MinLinks(g, f.MinLinks)
			},
		},
		{
			active: f.MaxLinks >= 0,
			flag:   "--max-links",
			build: func(g *Graph) Predicate {
				return MaxLinks(g, f.MaxLinks)
			},
		},
		{active: f.DeadEnd, flag: "--dead-end", build: DeadEnd},
	} {
		if !spec.active {
			continue
		}
		if f.Graph == nil {
			return nil, errors.New("query: " + spec.flag + " requires a graph")
		}
		ps = append(ps, spec.build(f.Graph))
	}
	return And(ps...), nil
}

// ParseAge parses an age expression like ">30d", "<7d", or "=0d". Supported
// units are s (seconds), m (minutes), h (hours), and d (days).
func ParseAge(s string) (op string, dur time.Duration, err error) {
	s = strings.TrimSpace(s)
	if len(s) < 3 {
		return "", 0, fmt.Errorf("invalid age expression %q: expect <op><n><unit>", s)
	}
	op = string(s[0])
	switch op {
	case ">", "<", "=":
	default:
		return "", 0, fmt.Errorf("invalid age expression %q: must start with >, <, or =", s)
	}
	rest := s[1:]
	unit := rest[len(rest)-1]
	n, parseErr := strconv.Atoi(rest[:len(rest)-1])
	if parseErr != nil {
		return "", 0, fmt.Errorf("invalid age value in %q: %w", s, parseErr)
	}
	if n < 0 {
		return "", 0, fmt.Errorf("invalid age value in %q: negative", s)
	}
	var mult time.Duration
	switch unit {
	case 's':
		mult = time.Second
	case 'm':
		mult = time.Minute
	case 'h':
		mult = time.Hour
	case 'd':
		mult = 24 * time.Hour
	default:
		return "", 0, fmt.Errorf("invalid age unit %q in %q: must be one of s, m, h, d", string(unit), s)
	}
	if int64(n) > int64(1<<63-1)/int64(mult) {
		return "", 0, fmt.Errorf("invalid age value in %q: overflows duration", s)
	}
	return op, time.Duration(n) * mult, nil
}

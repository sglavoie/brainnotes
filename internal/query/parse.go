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
	var ps []Predicate
	for _, t := range f.Tags {
		if t == "" {
			continue
		}
		ps = append(ps, Tag(t))
	}
	for _, c := range f.Categories {
		if c == "" {
			continue
		}
		ps = append(ps, Category(c))
	}
	if f.Status != "" {
		ps = append(ps, Status(f.Status))
	}
	if f.Type != "" {
		ps = append(ps, Type(f.Type))
	}
	if f.Has != "" {
		ps = append(ps, Has(f.Has))
	}
	if f.Missing != "" {
		ps = append(ps, Missing(f.Missing))
	}
	if f.Age != "" {
		op, dur, err := ParseAge(f.Age)
		if err != nil {
			return nil, fmt.Errorf("query: --age: %w", err)
		}
		ps = append(ps, Age(op, dur, "last"))
	}
	if f.Created != "" {
		op, dur, err := ParseAge(f.Created)
		if err != nil {
			return nil, fmt.Errorf("query: --created: %w", err)
		}
		ps = append(ps, Age(op, dur, "created"))
	}
	if f.Folder != "" {
		if f.Root == "" {
			return nil, errors.New("query: --folder requires a vault root path")
		}
		ps = append(ps, Folder(f.Folder, f.Root))
	}
	if f.Orphan {
		if f.Graph == nil {
			return nil, errors.New("query: --orphan requires a graph")
		}
		ps = append(ps, Orphan(f.Graph))
	}
	if f.BrokenLinks {
		if f.Graph == nil {
			return nil, errors.New("query: --broken-links requires a graph")
		}
		ps = append(ps, BrokenLinks(f.Graph))
	}
	if f.MinBacklinks > 0 {
		if f.Graph == nil {
			return nil, errors.New("query: --min-backlinks requires a graph")
		}
		ps = append(ps, MinBacklinks(f.Graph, f.MinBacklinks))
	}
	if f.MaxBacklinks >= 0 {
		if f.Graph == nil {
			return nil, errors.New("query: --max-backlinks requires a graph")
		}
		ps = append(ps, MaxBacklinks(f.Graph, f.MaxBacklinks))
	}
	if f.MinLinks > 0 {
		if f.Graph == nil {
			return nil, errors.New("query: --min-links requires a graph")
		}
		ps = append(ps, MinLinks(f.Graph, f.MinLinks))
	}
	if f.MaxLinks >= 0 {
		if f.Graph == nil {
			return nil, errors.New("query: --max-links requires a graph")
		}
		ps = append(ps, MaxLinks(f.Graph, f.MaxLinks))
	}
	if f.DeadEnd {
		if f.Graph == nil {
			return nil, errors.New("query: --dead-end requires a graph")
		}
		ps = append(ps, DeadEnd(f.Graph))
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
	return op, time.Duration(n) * mult, nil
}

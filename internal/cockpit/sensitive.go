package cockpit

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// sensitiveDetector pairs a stable, safe category label with the pattern that
// recognizes a secret-shaped value. The label is the only thing a task ever
// surfaces about a finding — the matched text is used solely to consult the
// ignore allow-list and is never stored on a task, so raw secrets never reach
// task JSON, signatures, or rendered web output.
type sensitiveDetector struct {
	name    string
	pattern *regexp.Regexp
}

// builtinSensitiveDetectors is the curated set of secret-shaped patterns the
// scanner recognizes. Patterns are compiled at package init (MustCompile is safe
// because they are constants); user-supplied ignore patterns are compiled at
// scan time with graceful error handling (compileSensitiveIgnores). Each label
// is deliberately generic so it categorizes a finding without echoing the value.
var builtinSensitiveDetectors = []sensitiveDetector{
	{name: "private key block", pattern: regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----`)},
	{name: "AWS access key ID", pattern: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{name: "GitHub token", pattern: regexp.MustCompile(`\bgh[pousr]_[0-9A-Za-z]{36,}\b`)},
	{name: "Slack token", pattern: regexp.MustCompile(`\bxox[baprs]-[0-9A-Za-z-]{10,}\b`)},
	{name: "Google API key", pattern: regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`)},
	{name: "JWT", pattern: regexp.MustCompile(`\beyJ[0-9A-Za-z_\-]{10,}\.[0-9A-Za-z_\-]{10,}\.[0-9A-Za-z_\-]{10,}\b`)},
	{name: "secret assignment", pattern: regexp.MustCompile(`(?i)\b(?:password|passwd|secret|token|api[_-]?key|apikey|access[_-]?key|private[_-]?key|client[_-]?secret|auth[_-]?token)\b\s*[:=]\s*['"]?[^\s'"]{6,}`)},
}

// sensitiveFinding is one detector hit at a body line. It carries only the line
// number and the detector's category label — never the matched text.
type sensitiveFinding struct {
	line     int
	detector string
}

// location renders a finding's redacted location string for an AffectedNote:
// the body line number and the detector category, with no matched value.
func (f sensitiveFinding) location() string {
	return fmt.Sprintf("line %d: %s", f.line, f.detector)
}

// sensitiveContentTasks builds one advisory task per note whose body appears to
// contain secret-shaped content. It is gated on [cockpit] sensitive_scan (off by
// default), so the cockpit never inspects note bodies unless explicitly asked.
// [cockpit] sensitive_ignore_paths skips whole notes by path prefix, and
// [cockpit] sensitive_ignore_patterns allow-lists individual matches. Findings
// expose only a redacted location (body line + detector category); the matched
// value is never placed on a task, so raw secrets stay out of task JSON,
// signatures, and rendered web output. Tasks are triage-only and never mutate a
// note (RuleSensitiveContent falls through to the triage-only action defaults).
func sensitiveContentTasks(notes []loadedNote, cfg *config.Config) []Task {
	if !cfg.Cockpit.SensitiveScan {
		return nil
	}
	ignorePaths := cfg.Cockpit.SensitiveIgnorePaths
	ignores := compileSensitiveIgnores(cfg.Cockpit.SensitiveIgnorePatterns)

	var tasks []Task
	for _, item := range notes {
		rel := filepath.ToSlash(item.rel)
		if sensitivePathIgnored(rel, ignorePaths) {
			continue
		}
		body, err := item.note.Body()
		if err != nil {
			continue
		}
		findings := scanSensitive(body, ignores)
		if len(findings) == 0 {
			continue
		}

		affected := make([]AffectedNote, 0, len(findings))
		for _, finding := range findings {
			affected = append(affected, affectedForNote(item, finding.location()))
		}
		title := affectedTitle(affected[0])
		tasks = append(tasks, newTask(
			RuleSensitiveContent,
			"Possible sensitive content: "+title,
			countSummary(len(findings), "possible secret in "+title, "possible secrets in "+title),
			rel,
			affected,
		))
	}
	return tasks
}

// scanSensitive returns the redacted findings for a note body: for each line,
// each built-in detector that matches yields one finding (at most one per
// detector per line). A match whose text is allow-listed by an ignore pattern is
// dropped. The returned findings carry only line numbers and detector labels.
func scanSensitive(body string, ignores []*regexp.Regexp) []sensitiveFinding {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	var findings []sensitiveFinding
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		for _, detector := range builtinSensitiveDetectors {
			for _, match := range detector.pattern.FindAllString(line, -1) {
				if ignoredMatch(match, ignores) {
					continue
				}
				findings = append(findings, sensitiveFinding{line: i + 1, detector: detector.name})
				break
			}
		}
	}
	return findings
}

// compileSensitiveIgnores compiles the configured allow-list patterns, skipping
// blank entries and any pattern that fails to compile. Session 4 owns this
// regexp error handling: an invalid ignore pattern is dropped rather than
// aborting the scan, so a typo in config cannot suppress the whole rule.
func compileSensitiveIgnores(patterns []string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		out = append(out, re)
	}
	return out
}

// ignoredMatch reports whether a matched value is allow-listed by any ignore
// pattern. The value is consulted here only; it is never returned or stored.
func ignoredMatch(value string, ignores []*regexp.Regexp) bool {
	for _, re := range ignores {
		if re.MatchString(value) {
			return true
		}
	}
	return false
}

// sensitivePathIgnored reports whether a vault-relative slash path falls under
// any configured ignore prefix. A prefix matches the note it names exactly or
// any descendant path, matching on slash boundaries so "Secrets" never masks
// "Secretsfoo.md". Blank prefixes are skipped.
func sensitivePathIgnored(rel string, prefixes []string) bool {
	for _, prefix := range prefixes {
		prefix = strings.Trim(filepath.ToSlash(strings.TrimSpace(prefix)), "/")
		if prefix == "" {
			continue
		}
		if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
			return true
		}
	}
	return false
}

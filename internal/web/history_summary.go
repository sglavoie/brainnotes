package web

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sglavoie/brainnotes/internal/journal"
)

// Summaries describe recorded actions, never infer an edit from today's note.
// Unknown commands get a neutral verb and remain inspectable in Details.
func historyActionSummary(entry journal.Entry, classifier historyPathClassifier) string {
	verb := historyActionVerb(entry.Command)
	paths := historySummaryPaths(entry, classifier, verb)
	if len(paths) == 1 {
		name := filepath.Base(paths[0])
		return verb + " " + strings.TrimSuffix(name, filepath.Ext(name))
	}
	if len(paths) > 1 {
		return fmt.Sprintf("%s %d notes", verb, len(paths))
	}
	return historyNonNoteSummary(entry.Command)
}

func historySummaryPaths(entry journal.Entry, classifier historyPathClassifier, verb string) []string {
	var paths []string
	for _, file := range entry.Files {
		if classifier.facts(file.Path).Class != historyPathNote {
			continue
		}
		// A move records the removed source and created destination; count the note
		// once, using its destination. Trash is described by the removed note instead.
		if (verb == "Moved" || verb == "Renamed") && (file.Before.Kind != journal.KindMissing || file.After.Kind != journal.KindFile) {
			continue
		}
		paths = append(paths, file.Path)
	}
	return paths
}

func historyActionVerb(command string) string {
	fields := strings.Fields(command)
	if len(fields) < 2 || fields[0] != "bn" {
		return "Changed"
	}
	fields = fields[1:]
	if fields[0] == "web" || fields[0] == "serve" {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return "Changed"
	}
	if fields[0] == "source" && len(fields) > 1 {
		fields = fields[1:]
	}
	verbs := map[string]string{
		"save": "Edited", "new": "Created", "create": "Created", "synthesize": "Created",
		"move": "Moved", "rename": "Renamed", "trash": "Trashed", "undo": "Restored",
		"capture": "Captured to", "set": "Updated properties in", "unset": "Updated properties in",
		"frontmatter": "Updated properties in", "property": "Updated properties in",
		"repair": "Repaired", "relink": "Repaired links in",
	}
	if verb, ok := verbs[fields[0]]; ok {
		return verb
	}
	return "Changed"
}

func historyNonNoteSummary(command string) string {
	rules := []struct{ prefix, summary string }{
		{"bn serve template save", "Saved template"}, {"bn serve template delete", "Deleted template"},
		{"bn serve setting", "Updated settings"}, {"bn config", "Updated configuration"},
		{"bn serve view", "Updated saved views"}, {"bn serve property schema", "Updated property definitions"},
		{"bn web undo", "Restored recorded changes"}, {"bn undo", "Restored recorded changes"},
		{"bn serve sync resolve", "Resolved sync changes"}, {"bn sync resolve", "Resolved sync changes"},
		{"bn sync apply", "Applied incoming changes"}, {"bn-vault-sync", "Synchronized changes"},
	}
	for _, rule := range rules {
		if strings.HasPrefix(command, rule.prefix) {
			return rule.summary
		}
	}
	return "Changed vault files"
}

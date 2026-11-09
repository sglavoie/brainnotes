// Package threads describes a portable Markdown collection. It never writes files.
package threads

import (
	"fmt"
	"regexp"
	"strings"
)

const Start = "<!-- bn:thread:v1 -->"
const End = "<!-- bn:thread:end -->"
const itemStart = "<!-- bn:thread:item "
const itemEnd = "<!-- bn:thread:item-end -->"
const MaxItems = 100

var Groups = []string{"Evidence", "Counterexamples", "Still unclear"}
var itemPattern = regexp.MustCompile(`(?s)^<!-- bn:thread:item ([a-f0-9]{64}) -->\n### (Evidence|Counterexamples|Still unclear)\n\nFrom (\[\[[^\n]+\]\])\n\n((?:> [^\n]*\n)*)\n(.*?)\n<!-- bn:thread:item-end -->$`)
var escapePattern = regexp.MustCompile("([\\\\`*_[\\]<>#~|+&.()-])")
var unescapePattern = regexp.MustCompile("\\\\([\\\\`*_[\\]<>#~|+&.()-])")

type Item struct {
	Group      string `json:"group"`
	Link       string `json:"link"`
	Quote      string `json:"quote"`
	Commentary string `json:"commentary"`
	Revision   string `json:"-"`
	raw        string
}

type Document struct {
	Before, After string
	Items         []Item
}

func NewBody(question string) string {
	return "## Question\n\n" + question + "\n\n" + Start + "\n\n" + End + "\n\n## Draft\n\n"
}

func IsThread(content string) bool { return strings.Contains(content, Start+"\n") }

// Parse refuses unrecognized collection bytes rather than dropping an external edit.
// Everything outside the collection is kept byte for byte, including frontmatter.
func Parse(content string) (Document, error) {
	if strings.Count(content, Start) != 1 || strings.Count(content, End) != 1 {
		return Document{}, fmt.Errorf("thread collection markers are missing or duplicated; edit the Markdown to repair them")
	}
	start, end := strings.Index(content, Start), strings.Index(content, End)
	if end < start {
		return Document{}, fmt.Errorf("thread collection markers are out of order")
	}
	d := Document{Before: content[:start], After: content[end+len(End):], Items: []Item{}}
	remaining := strings.TrimSpace(content[start+len(Start) : end])
	for remaining != "" {
		stop := strings.Index(remaining, itemEnd)
		if !strings.HasPrefix(remaining, itemStart) || stop < 0 {
			return Document{}, fmt.Errorf("unrecognized text in the collection; keep writing outside its markers")
		}
		raw := remaining[:stop+len(itemEnd)]
		item, err := parseItem(raw)
		if err != nil {
			return Document{}, err
		}
		d.Items = append(d.Items, item)
		if len(d.Items) > MaxItems {
			return Document{}, fmt.Errorf("a thread can hold at most %d passages", MaxItems)
		}
		remaining = strings.TrimSpace(remaining[len(raw):])
	}
	return d, nil
}

func parseItem(raw string) (Item, error) {
	m := itemPattern.FindStringSubmatch(raw)
	if m == nil {
		return Item{}, fmt.Errorf("a collected passage has an unrecognized format; repair it in the Markdown")
	}
	var quote []string
	if m[4] != "" {
		for _, line := range strings.Split(strings.TrimSuffix(m[4], "\n"), "\n") {
			quote = append(quote, strings.TrimPrefix(line, "> "))
		}
	}
	return Item{Group: m[2], Link: m[3], Quote: unescapePattern.ReplaceAllString(strings.Join(quote, "\n"), "$1"), Commentary: m[5], Revision: m[1], raw: raw}, nil
}

func (d Document) String() string {
	var blocks []string
	for _, item := range d.Items {
		blocks = append(blocks, item.raw)
	}
	middle := "\n\n"
	if len(blocks) > 0 {
		middle += strings.Join(blocks, "\n\n") + "\n\n"
	}
	return d.Before + Start + middle + End + d.After
}

func (d *Document) Add(item Item) error {
	if len(d.Items) >= MaxItems {
		return fmt.Errorf("a thread can hold at most %d passages", MaxItems)
	}
	raw, err := formatItem(item)
	if err != nil {
		return err
	}
	parsed, err := parseItem(raw)
	if err != nil {
		return err
	}
	d.Items = append(d.Items, parsed)
	return nil
}

func formatItem(item Item) (string, error) {
	if !validGroup(item.Group) {
		return "", fmt.Errorf("choose Evidence, Counterexamples, or Still unclear")
	}
	if strings.Contains(item.Commentary, "<!-- bn:thread:") {
		return "", fmt.Errorf("commentary cannot contain thread markers")
	}
	quote := ""
	if item.Quote != "" {
		for _, line := range strings.Split(strings.ReplaceAll(item.Quote, "\r\n", "\n"), "\n") {
			quote += "> " + escapePattern.ReplaceAllString(line, `\$1`) + "\n"
		}
	}
	return itemStart + item.Revision + " -->\n### " + item.Group + "\n\nFrom " + item.Link + "\n\n" + quote + "\n" + item.Commentary + "\n" + itemEnd, nil
}

func (d *Document) Move(index, delta int) error {
	next := index + delta
	if index < 0 || index >= len(d.Items) || next < 0 || next >= len(d.Items) || (delta != -1 && delta != 1) {
		return fmt.Errorf("invalid passage position")
	}
	d.Items[index], d.Items[next] = d.Items[next], d.Items[index]
	return nil
}

func (d *Document) Group(index int, group string) error {
	if index < 0 || index >= len(d.Items) || !validGroup(group) {
		return fmt.Errorf("invalid passage group")
	}
	item := &d.Items[index]
	item.raw = strings.Replace(item.raw, "\n### "+item.Group+"\n", "\n### "+group+"\n", 1)
	item.Group = group
	return nil
}

func validGroup(group string) bool {
	for _, candidate := range Groups {
		if group == candidate {
			return true
		}
	}
	return false
}

func (d *Document) Remove(index int) error {
	if index < 0 || index >= len(d.Items) {
		return fmt.Errorf("invalid passage position")
	}
	d.Items = append(d.Items[:index], d.Items[index+1:]...)
	return nil
}

func (d *Document) Comment(index int, commentary string) error {
	if index < 0 || index >= len(d.Items) {
		return fmt.Errorf("invalid passage position")
	}
	item := d.Items[index]
	item.Commentary = commentary
	raw, err := formatItem(item)
	if err != nil {
		return err
	}
	item.raw = raw
	d.Items[index] = item
	return nil
}

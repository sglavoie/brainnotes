// Package template renders note templates with a small FuncMap geared at the
// bnotes vault conventions (dates, slugs, wikilinks).
package template

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"
	"time"
	"unicode"
)

// Context is the data passed to a template execution. Fields are populated by
// the caller (typically the `new` command) before rendering; the FuncMap built
// per render references Context's Date/Time so the `now` and `today` template
// functions return the same instant as the Date/Time fields.
type Context struct {
	// Title is the user-supplied note title.
	Title string
	// Slug is a URL/file-friendly form of Title.
	Slug string
	// Folder is the resolved destination folder, vault-relative with forward slashes.
	Folder string
	// Vault is the absolute path to the vault root.
	Vault string
	// Date is the date used by the `today` function. Typically truncated to the day.
	Date time.Time
	// Time is the timestamp used by the `now` function.
	Time time.Time
	// Vars holds repeatable --var key=value pairs.
	Vars map[string]string
}

// Clock supplies the current time. The default implementation returns time.Now;
// tests can substitute a fixed clock to make `now`/`today` deterministic.
type Clock interface {
	Now() time.Time
}

// SystemClock returns time.Now on every call.
type SystemClock struct{}

// Now implements Clock.
func (SystemClock) Now() time.Time { return time.Now() }

// FixedClock returns the same time on every call. Intended for tests.
type FixedClock struct{ T time.Time }

// Now implements Clock.
func (c FixedClock) Now() time.Time { return c.T }

// NewContext builds a Context from primary inputs plus a Clock. The clock's
// Now() is used for both Time and Date (with Date truncated to midnight in the
// clock's location).
func NewContext(title, folder, vault string, vars map[string]string, clk Clock) Context {
	if clk == nil {
		clk = SystemClock{}
	}
	now := clk.Now()
	if vars == nil {
		vars = map[string]string{}
	}
	return Context{
		Title:  title,
		Slug:   Slug(title),
		Folder: folder,
		Vault:  vault,
		Time:   now,
		Date:   time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()),
		Vars:   vars,
	}
}

// Render parses and executes templateBody against ctx, returning the produced
// string. The FuncMap is documented in funcMap.
func Render(templateBody string, ctx Context) (string, error) {
	t, err := template.New("note").Funcs(funcMap(ctx)).Parse(templateBody)
	if err != nil {
		return "", fmt.Errorf("template: parse: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("template: execute: %w", err)
	}
	return buf.String(), nil
}

// funcMap builds the FuncMap exposed to templates. It captures ctx so the time
// functions are stable for a single render call.
func funcMap(ctx Context) template.FuncMap {
	return template.FuncMap{
		"now":        func() time.Time { return ctx.Time },
		"today":      func() string { return ctx.Date.Format("2006-01-02") },
		"dateFormat": func(layout string, t time.Time) string { return t.Format(layout) },
		"slug":       Slug,
		"title":      Title,
		"lower":      strings.ToLower,
		"upper":      strings.ToUpper,
		"default":    defaultFn,
		"env":        os.Getenv,
		"wikilink":   wikilinkFn,
	}
}

// defaultFn returns d when v is empty, otherwise v. Useful for template guards.
func defaultFn(d, v string) string {
	if v == "" {
		return d
	}
	return v
}

// wikilinkFn wraps s in [[ ]] unless it is already wrapped. The empty string
// is passed through unchanged so authors can guard with `default`. Whitespace
// around an already-wrapped value is preserved.
func wikilinkFn(s string) string {
	if s == "" {
		return ""
	}
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "[[") && strings.HasSuffix(trimmed, "]]") && len(trimmed) >= 4 {
		return s
	}
	return "[[" + s + "]]"
}

// Slug converts s to a lower-kebab-case slug: it lowercases Unicode letters,
// keeps Unicode digits, and folds runs of any other character into a single
// `-`. Leading and trailing dashes are trimmed. The output is not restricted
// to ASCII — input like "Café 2" yields "café-2".
func Slug(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevDash := true // suppresses a leading dash
	for _, r := range s {
		switch {
		case unicode.IsLetter(r):
			b.WriteRune(unicode.ToLower(r))
			prevDash = false
		case unicode.IsDigit(r):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := b.String()
	return strings.TrimRight(out, "-")
}

// Title returns s with the first rune of each whitespace-separated word
// upper-cased. Other runes are left as-is, so existing capitalisation inside
// a word is preserved.
func Title(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	atWordStart := true
	for _, r := range s {
		if unicode.IsSpace(r) {
			b.WriteRune(r)
			atWordStart = true
			continue
		}
		if atWordStart {
			b.WriteRune(unicode.ToUpper(r))
			atWordStart = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

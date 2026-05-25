package wikilink

import "testing"

func TestRewriteSkipsCodeBlocks(t *testing.T) {
	t.Parallel()
	in := "[[A]] real\n```\n[[A]] in code\n```\n[[A]] real again"
	got := Rewrite(in, func(l Link) (string, bool) {
		return "[[B]]", true
	})
	want := "[[B]] real\n```\n[[A]] in code\n```\n[[B]] real again"
	if got != want {
		t.Fatalf("Rewrite\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteQuotedCloserDoesNotCloseUnquotedFence(t *testing.T) {
	t.Parallel()

	in := "[[Foo]]\n```\n[[Foo]]\n> ```\n[[Foo]]"
	got := RewriteTitle(in, "Foo", "Bar")
	want := "[[Bar]]\n```\n[[Foo]]\n> ```\n[[Foo]]"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteSkipsMultilineInlineCodeSpan(t *testing.T) {
	t.Parallel()
	in := "before `[[Foo]]\n[[Foo]]` after [[Foo]]"
	got := RewriteTitle(in, "Foo", "Bar")
	want := "before `[[Foo]]\n[[Foo]]` after [[Bar]]"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteSkipsBlockquotedFencedCode(t *testing.T) {
	t.Parallel()
	in := "[[Foo]]\n> ```\n> [[Foo]]\n> ```\n[[Foo]]"
	got := RewriteTitle(in, "Foo", "Bar")
	want := "[[Bar]]\n> ```\n> [[Foo]]\n> ```\n[[Bar]]"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteSkipsBlockquotedIndentedCode(t *testing.T) {
	t.Parallel()
	in := "[[Foo]]\n>     [[Foo]]\n>     still code\n[[Foo]]"
	got := RewriteTitle(in, "Foo", "Bar")
	want := "[[Bar]]\n>     [[Foo]]\n>     still code\n[[Bar]]"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteInlineCodeSpanDoesNotCrossIntoBlockquote(t *testing.T) {
	t.Parallel()
	in := "`code\n> [[Foo]]`"
	got := RewriteTitle(in, "Foo", "Bar")
	want := "`code\n> [[Bar]]`"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteInlineCodeSpanDoesNotCrossOutOfNestedBlockquote(t *testing.T) {
	t.Parallel()
	in := "> > `code\n> [[Foo]]`"
	got := RewriteTitle(in, "Foo", "Bar")
	want := "> > `code\n> [[Bar]]`"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteInlineCodeSpanDoesNotCrossHeadingBoundary(t *testing.T) {
	t.Parallel()
	in := "`code\n# [[Foo]]`\n[[Foo]]"
	got := RewriteTitle(in, "Foo", "Bar")
	want := "`code\n# [[Bar]]`\n[[Bar]]"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
	}
}

func TestRewriteInlineCodeSpanDoesNotCrossListItemBoundary(t *testing.T) {
	t.Parallel()
	in := "`code\n- [[Foo]]`\n[[Foo]]"
	got := RewriteTitle(in, "Foo", "Bar")
	want := "`code\n- [[Bar]]`\n[[Bar]]"
	if got != want {
		t.Fatalf("RewriteTitle\n  got:  %q\n  want: %q", got, want)
	}
}

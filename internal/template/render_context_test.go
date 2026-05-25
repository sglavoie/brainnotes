package template

import (
	"testing"
	"time"
)

func TestNewContextNilVars(t *testing.T) {
	t.Parallel()
	ctx := NewContext("t", "", "/v", nil, nil)
	if ctx.Vars == nil {
		t.Fatal("Vars must not be nil")
	}
}

func TestNewContextDateTruncation(t *testing.T) {
	t.Parallel()
	// Time with non-zero hours/minutes/seconds should truncate to midnight.
	clk := FixedClock{T: time.Date(2024, 12, 25, 14, 30, 59, 123456789, time.UTC)}
	ctx := NewContext("Christmas", "", "/v", nil, clk)
	want := time.Date(2024, 12, 25, 0, 0, 0, 0, time.UTC)
	if !ctx.Date.Equal(want) {
		t.Fatalf("Date = %v, want %v", ctx.Date, want)
	}
	// Time field should preserve full precision.
	if !ctx.Time.Equal(clk.T) {
		t.Fatalf("Time = %v, want %v", ctx.Time, clk.T)
	}
}

func TestNewContextSlugGeneration(t *testing.T) {
	t.Parallel()
	ctx := NewContext("My Great Note", "", "/v", nil, nil)
	if ctx.Slug != "my-great-note" {
		t.Fatalf("Slug = %q, want %q", ctx.Slug, "my-great-note")
	}
}

func TestRenderAllFieldsAccessible(t *testing.T) {
	t.Parallel()
	clk := FixedClock{T: time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)}
	ctx := NewContext("Test Note", "subfolder", "/my/vault", map[string]string{"k": "v"}, clk)
	body := "T={{.Title}} S={{.Slug}} F={{.Folder}} V={{.Vault}} K={{.Vars.k}}"
	got := mustRender(t, body, ctx)
	want := "T=Test Note S=test-note F=subfolder V=/my/vault K=v"
	if got != want {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

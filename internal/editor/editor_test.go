package editor

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestSpec(t *testing.T) {
	t.Run("configured takes precedence", func(t *testing.T) {
		got, err := Spec("nvim", "vim")
		if err != nil {
			t.Fatalf("Spec returned error: %v", err)
		}
		if got != "nvim" {
			t.Fatalf("Spec returned %q, want %q", got, "nvim")
		}
	})

	t.Run("env fallback", func(t *testing.T) {
		got, err := Spec("", "vim")
		if err != nil {
			t.Fatalf("Spec returned error: %v", err)
		}
		if got != "vim" {
			t.Fatalf("Spec returned %q, want %q", got, "vim")
		}
	})

	t.Run("missing editor", func(t *testing.T) {
		_, err := Spec("", "")
		if !errors.Is(err, ErrNoEditorConfigured) {
			t.Fatalf("Spec error = %v, want ErrNoEditorConfigured", err)
		}
	})
}

func TestResolve_ParsesEditorArgsAndQuotes(t *testing.T) {
	origLookPath := lookPath
	t.Cleanup(func() { lookPath = origLookPath })
	lookPath = func(file string) (string, error) { return "/mock/" + file, nil }

	bin, argv, err := Resolve(`code --wait "foo bar"`, "/vault/Note.md", 0)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if bin != "/mock/code" {
		t.Fatalf("bin = %q, want %q", bin, "/mock/code")
	}
	want := []string{"code", "--wait", "foo bar", "/vault/Note.md"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
}

func TestResolve_UnterminatedQuote(t *testing.T) {
	_, _, err := Resolve(`code "unterminated`, "/vault/Note.md", 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unterminated quote") {
		t.Fatalf("error = %v, want unterminated quote", err)
	}
}

func TestResolve_LineTargets(t *testing.T) {
	origLookPath := lookPath
	t.Cleanup(func() { lookPath = origLookPath })
	lookPath = func(file string) (string, error) { return "/mock/" + file, nil }

	tests := []struct {
		name string
		spec string
		want []string
	}{
		{
			name: "terminal editor",
			spec: "nvim --clean",
			want: []string{"nvim", "--clean", "+12", "/vault/Note.md"},
		},
		{
			name: "vscode adds goto",
			spec: "code --wait",
			want: []string{"code", "--wait", "-g", "/vault/Note.md:12"},
		},
		{
			name: "vscode keeps goto",
			spec: "code --goto",
			want: []string{"code", "--goto", "/vault/Note.md:12"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, argv, err := Resolve(tt.spec, "/vault/Note.md", 12)
			if err != nil {
				t.Fatalf("Resolve returned error: %v", err)
			}
			if !reflect.DeepEqual(argv, tt.want) {
				t.Fatalf("argv = %v, want %v", argv, tt.want)
			}
		})
	}
}

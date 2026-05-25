package template

import "testing"

func mustRender(t *testing.T, body string, ctx Context) string {
	t.Helper()
	got, err := Render(body, ctx)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return got
}

func mustRenderError(t *testing.T, body string, ctx Context) error {
	t.Helper()
	_, err := Render(body, ctx)
	if err == nil {
		t.Fatal("expected render error")
	}
	return err
}

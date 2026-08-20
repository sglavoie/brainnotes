package recall

import (
	"reflect"
	"testing"
)

func TestStandardPurposesAreCompleteAndSelfNaming(t *testing.T) {
	t.Parallel()

	names := StandardPurposeNames()
	want := []string{PurposeSynthesis, PurposeContradictions, PurposeQuestions}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("standard purpose names = %v, want %v", names, want)
	}
	if len(standardPurposes) != len(want) {
		t.Fatalf("standard purposes = %d, want %d", len(standardPurposes), len(want))
	}
	for _, name := range names {
		purpose, ok := StandardPurpose(name)
		if !ok {
			t.Fatalf("standard purpose %q is not defined", name)
		}
		if purpose.Name != name {
			t.Errorf("purpose %q reports name %q", name, purpose.Name)
		}
		if purpose.Instructions == "" {
			t.Errorf("purpose %q has no instructions", name)
		}
	}
	if _, ok := StandardPurpose("brainstorm"); ok {
		t.Error("unknown purpose name resolved")
	}
}

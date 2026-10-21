package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vaultimport"
)

func decodeSyncJSON(t *testing.T, body string, into any) {
	t.Helper()

	if err := json.Unmarshal([]byte(body), into); err != nil {
		t.Fatalf("decode %T from %s: %v", into, body, err)
	}
}

// operationNamed finds one operation in the review payload.
func operationNamed(t *testing.T, view syncConflictsView, packageID string) syncConflictOperationView {
	t.Helper()

	for _, operation := range view.Operations {
		if operation.PackageID == packageID {
			return operation
		}
	}
	t.Fatalf("no operation for %s in %d reported: %+v", packageID, len(view.Operations), view.Operations)
	return syncConflictOperationView{}
}

// pathNamed finds one contested path of an operation.
func pathNamed(t *testing.T, operation syncConflictOperationView, rel string) syncContestedPathView {
	t.Helper()

	for _, path := range operation.Paths {
		if path.Path == rel {
			return path
		}
	}
	t.Fatalf("%s does not contest %q: %+v", operation.PackageID, rel, operation.Paths)
	return syncContestedPathView{}
}

// choiceNamed finds one choice among a path's available or refused ones.
func choiceNamed(t *testing.T, path syncContestedPathView, choice string) (syncChoiceView, bool) {
	t.Helper()

	for _, available := range path.Available {
		if available.Choice == choice {
			return available, true
		}
	}
	for _, refused := range path.Refused {
		if refused.Choice == choice {
			return refused, false
		}
	}
	t.Fatalf("%q offers neither %s nor a reason for refusing it: %+v", path.Path, choice, path)
	return syncChoiceView{}, false
}

// assertVersionsShown fails unless a contested path carries both versions, which
// is the claim the whole review surface rests on.
func assertVersionsShown(t *testing.T, path syncContestedPathView, local, remote string) {
	t.Helper()

	if path.Local.Text != local {
		t.Fatalf("%s: this vault's version = %q, want %q", path.Path, path.Local.Text, local)
	}
	if path.Remote.Text != remote {
		t.Fatalf("%s: the peer's version = %q, want %q", path.Path, path.Remote.Text, remote)
	}
}

// assertChoicesOffered fails unless each named choice is available and worded. An
// offered choice with no sentence is a radio button nobody can act on.
func assertChoicesOffered(t *testing.T, path syncContestedPathView, choices ...vaultimport.Choice) {
	t.Helper()

	for _, choice := range choices {
		view, available := choiceNamed(t, path, string(choice))
		if !available {
			t.Fatalf("%s: %s is not on offer", path.Path, choice)
		}
		if view.Label == "" || view.Effect == "" {
			t.Fatalf("%s: %s is offered with nothing said about it: %+v", path.Path, choice, view)
		}
	}
}

// assertEffectsAreContentNeutral fails when a choice offered over content this
// page could not show describes itself as a decision about text.
//
// "Text" is the whole test because it is the whole defect: every kind-specific
// sentence in sync_choice_wording.go is written about a note somebody is reading,
// and a person shown two digests and told one of them "keeps the text it has here"
// has been invited to weigh something they were never given.
func assertEffectsAreContentNeutral(t *testing.T, path syncContestedPathView) {
	t.Helper()

	for _, choice := range path.Available {
		if strings.Contains(strings.ToLower(choice.Effect), "text") {
			t.Fatalf("%s: %s is worded as a decision about text: %q",
				path.Path, choice.Choice, choice.Effect)
		}
	}
}

// assertChoiceWorded fails unless a choice is offered carrying exactly the
// sentence it is supposed to carry, and returns it for whatever else a test asks.
// complaint says what a mismatch would mean, because two long sentences printed
// side by side say which characters differ and never what that costs a person.
func assertChoiceWorded(
	t *testing.T,
	path syncContestedPathView,
	choice vaultimport.Choice,
	want, complaint string,
) syncChoiceView {
	t.Helper()

	view, available := choiceNamed(t, path, string(choice))
	if !available {
		t.Fatalf("%s: %s is not on offer, so the engine's own answer changed: %+v",
			path.Path, choice, path)
	}
	if view.Effect != want {
		t.Fatalf("%s: %s: %s: %q", path.Path, choice, complaint, view.Effect)
	}
	return view
}

// assertNamesTheVersionItDiscards is the other half of the claim above, and it is
// here rather than inline because the two are one claim: neutral wording is about
// the *effect* sentence only, and the version that stops being in the vault is
// still named. A page that went neutral on both would be asking for a destructive
// decision without saying anything was destroyed.
func assertNamesTheVersionItDiscards(t *testing.T, path syncContestedPathView) {
	t.Helper()

	for choice, discards := range map[vaultimport.Choice]string{
		vaultimport.KeepCurrent:    syncDiscardsRemote,
		vaultimport.AcceptIncoming: syncDiscardsLocal,
	} {
		view, _ := choiceNamed(t, path, string(choice))
		if view.Discards != discards {
			t.Fatalf("%s: %s discards %q, want %q", path.Path, choice, view.Discards, discards)
		}
	}
}

// errorCodeOf reads the management envelope's code, which is what a client acts
// on and is the thing these tests assert rather than the sentence.
func errorCodeOf(t *testing.T, body string) string {
	t.Helper()

	var envelope struct {
		OK      bool   `json:"ok"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	decodeSyncJSON(t, body, &envelope)
	if envelope.OK || envelope.Code == "" {
		t.Fatalf("expected a refusal naming a code, got %s", body)
	}
	return envelope.Code
}

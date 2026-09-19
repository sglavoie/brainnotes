package web

import "github.com/sglavoie/sglavoie_brainnotes/internal/vaultimport"

// This file is the wording of a conflict and of the four decisions about it, and
// it is in Go rather than in the page for one reason: what "keep mine" does to a
// deleted note is a statement about the synchronization engine, not about layout.
// Keeping it here means a Go test can hold each sentence to the choice it
// describes, and that the CLI could say the same thing without translating a
// template.
//
// Every sentence is in the indicative and about this vault: it says what will be
// true afterwards, never how careful to be. The one judgement carried is
// syncChoiceView.Discards, which names the version that stops being here.

// syncConflictSummaries is what each kind of disagreement is.
var syncConflictSummaries = map[vaultimport.ConflictKind]string{
	vaultimport.ConflictConcurrentEdit: "Both replicas changed this note.",
	vaultimport.ConflictDeleteEdit:     "The other replica deleted this note and this one changed it.",
	vaultimport.ConflictEditDelete:     "The other replica changed this note and this one deleted it.",
	vaultimport.ConflictDivergentRename: "The other replica renamed this note, and it is no longer " +
		"where it was here.",
	vaultimport.ConflictFilenameCollision: "This vault already holds this name under a spelling that " +
		"is the same file on the other replica.",
	vaultimport.ConflictConfiguration: "Both replicas changed the declarations that govern every " +
		"note in this vault.",
}

// syncConflictRemedies is what to do about a conflict no choice can settle.
//
// One kind is here, because one kind refuses all four: a filename collision is
// true whatever either version says, so the work is in the vault and the change
// waits in the channel until it is done. The sentence is therefore about renaming
// a file rather than about anything on this page. Every other kind leaves at
// least "take the other replica's version" open — that choice adds no revision at
// all, so nothing but a collision can refuse it — and a path with no choice and no
// remedy would be a dead end.
var syncConflictRemedies = map[vaultimport.ConflictKind]string{
	vaultimport.ConflictFilenameCollision: "Rename or remove the file already here so the two names " +
		"are no longer one name, and this change will be applied on the next pass.",
}

// syncChoiceLabels is each choice as a control says it.
var syncChoiceLabels = map[vaultimport.Choice]string{
	vaultimport.KeepCurrent:    "Keep this vault's version",
	vaultimport.AcceptIncoming: "Take the other replica's version",
	vaultimport.KeepBoth:       "Keep both",
	vaultimport.ManualMerge:    "Merge by hand",
}

// syncChoiceLabelOrder is the order refused choices are listed in, which is the
// order the engine presents the available ones in.
var syncChoiceLabelOrder = []vaultimport.Choice{
	vaultimport.KeepCurrent,
	vaultimport.AcceptIncoming,
	vaultimport.KeepBoth,
	vaultimport.ManualMerge,
}

// syncChoiceEffect is what one choice does to one kind of conflict.
type syncChoiceEffect struct {
	effect   string
	discards string
}

// syncDiscardsLocal and syncDiscardsRemote name the side a choice decides
// against. They are the two values of syncChoiceView.Discards.
const (
	syncDiscardsLocal  = "local"
	syncDiscardsRemote = "remote"
)

// syncChoiceEffects is the effect of each choice, per kind, and is deliberately
// sparse: a combination the conflict cannot take is refused with the engine's own
// reason and never reaches here, so only the offerable ones are worth wording.
// syncGenericEffects answers a combination this table does not name.
var syncChoiceEffects = map[vaultimport.ConflictKind]map[vaultimport.Choice]syncChoiceEffect{
	vaultimport.ConflictConcurrentEdit: {
		vaultimport.KeepCurrent: {
			effect:   "This note keeps the text it has here. The other replica's version will not be in this vault.",
			discards: syncDiscardsRemote,
		},
		vaultimport.AcceptIncoming: {
			effect:   "This note takes the other replica's text. The text it has here is replaced.",
			discards: syncDiscardsLocal,
		},
		vaultimport.KeepBoth: {
			effect: "This note takes the other replica's text, and the text it has here is written " +
				"beside it as a conflicted copy.",
		},
		vaultimport.ManualMerge: {
			effect: "This note takes the text you assemble, in place of both versions.",
		},
	},
	vaultimport.ConflictDeleteEdit: {
		vaultimport.KeepCurrent: {
			effect: "This note stays, with the changes made here. The other replica's deletion is " +
				"rejected and the note will come back there.",
			discards: syncDiscardsRemote,
		},
		vaultimport.AcceptIncoming: {
			effect: "This note is deleted here, including the changes made to it that the other " +
				"replica has never seen.",
			discards: syncDiscardsLocal,
		},
		vaultimport.ManualMerge: {
			effect: "This note takes the text you assemble, in place of both the version here and " +
				"the other replica's deletion.",
		},
	},
	vaultimport.ConflictEditDelete: {
		vaultimport.KeepCurrent: {
			effect: "This note stays deleted here. The other replica's version is rejected and the " +
				"note will be deleted there too.",
			discards: syncDiscardsRemote,
		},
		vaultimport.AcceptIncoming: {
			effect: "This note is created again, with the other replica's text. Nothing that is " +
				"here now is lost, because nothing is here.",
		},
		vaultimport.ManualMerge: {
			effect: "This note is created again, with the text you assemble.",
		},
	},
	vaultimport.ConflictDivergentRename: {
		vaultimport.KeepCurrent: {
			effect: "The arrangement here is left alone. The other replica's rename is rejected and " +
				"it will follow this vault instead.",
			discards: syncDiscardsRemote,
		},
		vaultimport.AcceptIncoming: {
			effect: "The note is written under the name the other replica chose. Whatever this vault " +
				"did with it stays as it is, so check for a second copy.",
		},
	},
	vaultimport.ConflictConfiguration: {
		vaultimport.KeepCurrent: {
			effect: "These declarations keep what they say here, which governs every note in this " +
				"vault. The other replica's change to them is rejected.",
			discards: syncDiscardsRemote,
		},
		vaultimport.AcceptIncoming: {
			effect: "These declarations take what the other replica says, which governs every note " +
				"in this vault — including notes this change does not carry.",
			discards: syncDiscardsLocal,
		},
		vaultimport.ManualMerge: {
			effect: "These declarations take what you assemble, which governs every note in this vault.",
		},
	},
}

// syncGenericEffects is the fallback, and it says less on purpose. A kind this
// file has not been taught is described in the vocabulary of the choice alone
// rather than guessed at, so a kind added to the engine reads as vague here
// instead of reading as wrong.
var syncGenericEffects = map[vaultimport.Choice]syncChoiceEffect{
	vaultimport.KeepCurrent: {
		effect:   "What is here is kept. The other replica's version is rejected.",
		discards: syncDiscardsRemote,
	},
	vaultimport.AcceptIncoming: {
		effect:   "The other replica's version is taken. What is here is replaced.",
		discards: syncDiscardsLocal,
	},
	vaultimport.KeepBoth: {
		effect: "The other replica's version is taken, and what is here is written beside it as a " +
			"conflicted copy.",
	},
	vaultimport.ManualMerge: {
		effect: "What you assemble is written, in place of both versions.",
	},
}

// syncAvailableChoice is one offerable choice as a person reads it.
func syncAvailableChoice(kind vaultimport.ConflictKind, choice vaultimport.Choice) syncChoiceView {
	effect, worded := syncChoiceEffects[kind][choice]
	if !worded {
		effect = syncGenericEffects[choice]
	}
	return syncChoiceView{
		Choice:   string(choice),
		Label:    syncChoiceLabels[choice],
		Effect:   effect.effect,
		Discards: effect.discards,
	}
}

# Browser UI qualification

Use a disposable vault and a build from the revision being qualified. Keep the
result with release evidence or the pull request. A viewport simulation cannot
replace the phone pass: the operating system keyboard changes the visible page.

From the repository root, after `npm ci`, run:

```sh
python3 scripts/ui_qualification.py --port 8765
```

For a release candidate, commit the final source and generated assets, then run
from that clean checkout:

```sh
python3 scripts/ui_qualification.py --release --record /tmp/brainnotes-ui-qualification.json --port 8765
```

The launcher refuses a dirty release checkout, checks that source files did not
change during compilation, and writes the commit, source SHA-256, binary SHA-256,
serving mode, and start time. The record must be a new file outside the checkout.
Its device and observation fields start as `null`: fill them with observed results,
then preserve the completed record with release evidence. A build manifest alone
does not establish a pass. Use a new record and repeat qualification if the tested
source changes. Development runs can also use `--record`; their working-tree
state is recorded and they are explicitly not release candidates.

The launcher rebuilds the browser assets and binary, initializes and verifies a
temporary vault, seeds a long Writing note with frontmatter and an attachment,
then serves it in hosted mode. It prints a localhost URL, a phone URL, and the
binary SHA-256 to record below. Open the phone URL from the same trusted local
network. If the printed address is a VPN address, use the computer's LAN IP with
the printed port. Stop with Ctrl-C; the temporary fixture is removed. Hosted
mode has no built-in sign-in, so keep this disposable server on the local
network only.

## Record

- HEAD commit, working-tree state, date, and printed binary SHA-256:
- Vault fixture and serving mode:
- Physical phone model, OS version, browser version, orientation, and text size:
- Desktop OS, browser version, screen reader and version:
- Result for each journey below: pass or fail, with a screenshot or short recording
  for any failure and the fixing commit:

Record the exact larger-text setting, not just “larger text.” The historical
2026-10-19 record describes its own uncommitted build and does not qualify a
later commit.

## Physical phone: writing

1. Open a long note with frontmatter and an attachment. With the keyboard open,
   type near the bottom. Check that the caret, new text, and Save stay reachable
   without horizontal scrolling in portrait and landscape.
2. Switch between Edit and Preview, then use Format and Find. Check that the
   same passage and unsaved draft remain available.
3. Save and reopen the note. Repeat with the connection interrupted, then
   reconnect and save. Check that an uncertain save never loses the draft.
4. Repeat the writing and toolbar checks with larger system text.

## Phone: navigation and layout

1. From reading, editing, and Synthesis, use the Search shortcut without opening
   Workspace. Find and reopen a note. When leaving an unsaved editor, check that
   its existing draft protection still applies.
2. Search Library with text and a filter. Check that the compact controls keep
   the active constraints visible and leave more room for results. Open Filters,
   Search actions, and Query expression using touch and keyboard navigation.
3. Open Graph. Check that the canvas is visible before the selected-note details.
   Open Refine graph and use a filter or layout control; verify that its active
   marker remains visible when the controls are collapsed.
4. In Synthesis, follow Prepare context, then write and preview a draft. Check
   that the context step changes from “not prepared” to “ready.”
5. Preview a History restore and verify that the recorded date has a named month,
   full year, and time-zone offset.

## Screen reader: finding and writing

With VoiceOver and Safari on macOS, navigate by landmarks and headings through
Library, note reading, and editing. Search for a note, open it, edit it, and
save a small change. Check the spoken name and state of each control, the order
and content of status announcements, and focus after opening disclosures and
completing Save. Also follow a sync review link when one is present and use the
Synthesis **Prepare context** action from a phone-sized viewport. Record any
announcement that is missing, repeated, or misleading.

The journey is complete only when every field in the record is known and every
step has an observed result. An older pass for a different build is useful
history, but it does not qualify the current UI.

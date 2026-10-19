# Browser UI qualification

Use a disposable vault and a build from the revision being qualified. Keep the
result with release evidence or the pull request. A viewport simulation cannot
replace the phone pass: the operating system keyboard changes the visible page.

From the repository root, after `npm ci`, run:

```sh
python3 scripts/ui_qualification.py --port 8765
```

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

## Physical phone: writing

1. Open a long note with frontmatter and an attachment. With the keyboard open,
   type near the bottom. Check that the caret, new text, and Save stay reachable
   without horizontal scrolling in portrait and landscape.
2. Switch between Edit and Preview, then use Format and Find. Check that the
   same passage and unsaved draft remain available.
3. Save and reopen the note. Repeat with the connection interrupted, then
   reconnect and save. Check that an uncertain save never loses the draft.
4. Repeat the writing and toolbar checks with larger system text.

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

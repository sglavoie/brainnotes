# Mobile editor qualification record

Use a physical iOS or Android phone and a disposable vault. Record one result
per browser and orientation after any change to editor layout, scrolling, or
toolbar placement. The browser suite exercises narrow viewports but cannot show
how the operating system keyboard resizes the page.

## Session

- Date: 2026-10-18
- Device and screen size: physical iPhone, 1170 × 2532 screenshot; model not reported
- OS and browser versions: not reported
- Brain Notes commit: `348905b` plus uncommitted editor changes
- Vault: disposable `bn-mobile-qualification.RhYAcj/vault` fixture under the system temporary directory
- Result: pass after fixing the portrait width and landscape writing height

## Journey

1. Open a long note with frontmatter and an attachment. Tap the prose near the
   bottom and type two paragraphs with the keyboard open. Record whether the
   caret and newly typed text remain visible and whether Save remains reachable.
2. Switch Edit to Preview and back. Confirm the same passage remains in view and
   the draft is still present.
3. Open Format and Find, search for a phrase, close each control, and keep typing.
   Confirm neither toolbar covers the selected text or traps focus.
4. Save, reopen the note, and verify the new paragraphs. Repeat a failed save
   with the connection interrupted; the draft must remain available.
5. Repeat steps 1–3 in landscape and with larger system text.

## Evidence and follow-up

- Portrait result and screenshot or short recording: User screenshot from 20:50
  shows Format open and “Saved to vault.” while Save and the editor extend past
  the right edge. The user reported horizontal scrolling. The screenshot is
  retained in the test conversation, outside the repository. After the editor
  CSS was rebuilt and the disposable server restarted, the user refreshed,
  typed, saved, opened Format, and confirmed Save remained fully visible
  without horizontal scrolling.
- Landscape result and screenshot or short recording: The user reported that
  the typing area was too short top-to-bottom. Its 55vh cap measured about
  200–215px on short landscape viewports. After raising it to 80vh with an
  18rem minimum, the user refreshed and typed with the keyboard open and
  reported that it works. No screenshot was supplied for this retest.
- Edit/Preview and Find result: The user confirmed the round trip and Find
  while typing worked on the physical iPhone.
- Larger text result and screenshot or short recording: The user confirmed it
  worked on the physical iPhone; no screenshot was supplied.
- Failed save result: The user confirmed the draft remained available after a
  Save with Wi-Fi off, then saved after reconnecting.
- Issues found, reproduction steps, and fixing commit: The toolbar used four
  columns down to a 16rem container, and the focused CodeMirror surface used
  14.4px text. The current working tree reflows the controls earlier and uses
  16px editor text on phone layouts. The landscape writing surface now has a
  larger height on short screens. The physical journey passed after both
  layout fixes. A commit has not been created.

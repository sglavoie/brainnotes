# Browser UI qualification record — 2026-10-19

This record follows the [browser UI qualification checklist](ui-qualification.md).
The results are the user's observations on the build below.
This is historical evidence for that build only. Its uncommitted source and
unspecified larger-text setting do not qualify the current checkout or a release.

## Build and environment

- HEAD: `e96112e9b36c5abb2490ad79acc73090c50db528`; uncommitted working tree
  (67 changed or untracked paths when the server started).
- Served binary SHA-256: `b45aa53933758b62628cc4fbdc8d7825e1411366cfaac1079e3e8163e2d3aa5e`.
- Vault: disposable `bn-ui-qualification-2ejmpbat/vault` fixture under the
  system temporary directory, served in hosted mode on the local network.
- Phone: physical iPhone 13 Pro, iOS 27.0.1, Safari 27.0.1; portrait and
  landscape; normal and larger system text. The exact larger text setting was
  not reported.
- Desktop: macOS 27.0.1, Safari 27.0.1, VoiceOver 10.

## Observed results

The user confirmed that the full checklist passed on this build:

- With the phone keyboard open, the caret, newly typed text, and Save stayed
  visible and reachable near the bottom of the long Writing note in portrait
  and landscape, without horizontal scrolling.
- Edit/Preview, Format, and Find preserved the passage and unsaved draft.
- Saving and reopening worked. An interrupted save kept the draft, and saving
  after reconnecting worked.
- Writing and toolbar checks passed with larger system text.
- The macOS Safari/VoiceOver journey through Library, reading, search, editing,
  and Save passed, including control announcements, status order, disclosure
  and Save focus, the conditional sync review link, and the Synthesis **Prepare
  context** action at a phone-sized viewport.

No failures or recordings were reported for this session. The user confirmed
the full checklist in the test conversation; no screenshots were supplied.

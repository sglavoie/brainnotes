package main

import (
	"context"
	"strings"
	"time"

	"github.com/sglavoie/brainnotes/internal/agent"
)

// notifyTimeout bounds the osascript call. A notification is a courtesy: if the
// notification centre is wedged, the guard must keep watching the vault rather
// than wait on it.
const notifyTimeout = 10 * time.Second

// notifyDesktop raises a macOS notification.
//
// Its context is deliberately not the guard's. A notification is most useful at
// exactly the moment the guard is being shut down or is about to be, and
// inheriting a cancelled context would suppress the one message a person needed
// to see.
//
// Failure is silent by design. There is no supervisor above this process to
// report to, the log already carries the same event with more detail, and a
// guard that exited because it could not draw a banner would have traded a
// missed notification for a vault nobody is watching.
func notifyDesktop(title, body string) {
	ctx, cancel := context.WithTimeout(context.Background(), notifyTimeout)
	defer cancel()
	script := "display notification " + appleScriptString(body) +
		" with title " + appleScriptString(title)
	_, _ = agent.Run(ctx, []string{"osascript", "-e", script}, nil)
}

// appleScriptString quotes a Go string as an AppleScript literal.
//
// Every string this file is given today is generated from a fixed vocabulary
// plus an integer, so nothing here can currently need escaping. It escapes
// anyway: the argument would otherwise be one unlucky future caller away from
// being a script-injection sink, and osascript's `-e` is interpreted, not
// argv-quoted. Control characters are dropped rather than escaped because
// AppleScript has no portable spelling for them and a notification cannot show
// them.
func appleScriptString(value string) string {
	var b strings.Builder
	b.Grow(len(value) + 2)
	b.WriteByte('"')
	for _, r := range value {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

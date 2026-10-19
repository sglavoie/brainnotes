package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// interactiveChildren counts the foreground children (fzf) that own the
// terminal right now. The terminal delivers Ctrl-C to the whole process group,
// and while fzf is up that keystroke belongs to fzf: it maps it to its own exit
// 130, which RunFZF already turns into ErrFZFInterrupted. Cancelling bn's
// context as well would make the same keystroke mean two things.
var interactiveChildren atomic.Int32

// withInteractiveChild runs fn while SIGINT is left to the foreground child.
// SIGTERM still cancels: nobody types it at a picker.
func withInteractiveChild(fn func() error) error {
	interactiveChildren.Add(1)
	defer interactiveChildren.Add(-1)
	return fn()
}

// stillFinishingAfter is how long a cancelled command may take to return
// before bn says why the first Ctrl-C did not end it.
const stillFinishingAfter = 2 * time.Second

// notifyInterrupt returns a context that SIGINT or SIGTERM cancels.
//
// The first signal only cancels: a command in the middle of a journaled write
// or a graceful server shutdown gets to finish what it promised. Delivery is
// then handed back to the default handler, so a second Ctrl-C ends the process
// at once — the escape hatch for a command that does not watch its context.
func notifyInterrupt(stderr io.Writer) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		for {
			select {
			case <-ctx.Done():
				signal.Stop(signals)
				return
			case sig := <-signals:
				if sig == os.Interrupt && interactiveChildren.Load() > 0 {
					continue
				}
				signal.Stop(signals)
				cancel()
				warnStillFinishing(stderr)
				return
			}
		}
	}()
	return ctx, cancel
}

func warnStillFinishing(stderr io.Writer) {
	time.AfterFunc(stillFinishingAfter, func() {
		_, _ = fmt.Fprintln(stderr, "bn: interrupted; still finishing (press Ctrl-C again to stop immediately)")
	})
}

// commandContext is cmd's context, which carries the interrupt cancellation, or
// a background context for a command run outside Execute (as tests do).
func commandContext(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}

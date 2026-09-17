// Command gws-cache keeps a local copy of Gmail threads and Google Calendar
// events so they can be searched and exported without network round trips.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/alexgorbatchev/gws-cache-cli/internal/cli"
)

// version is injected at build time via -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	// Ctrl-C cancels the in-flight request rather than killing the process
	// part way through a write.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Cobra prints the error itself, so there is nothing left to report here.
	if err := cli.NewRootCmd(version).ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}

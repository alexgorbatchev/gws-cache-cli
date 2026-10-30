// Package cli builds the gws-cache command tree.
package cli

import (
	"context"
	"fmt"
	"time"

	cobrahelptree "github.com/alexgorbatchev/cobra-help-tree"
	"github.com/spf13/cobra"

	"github.com/alexgorbatchev/gws-cache-cli/internal/classifier"
	"github.com/alexgorbatchev/gws-cache-cli/internal/gws"
	"github.com/alexgorbatchev/gws-cache-cli/internal/store"
)

// defaultTimeout bounds a single gws invocation so that an unresponsive
// Google Workspace CLI cannot hang the cache indefinitely.
const defaultTimeout = 2 * time.Minute

// app holds the settings shared by every subcommand.
type app struct {
	dbPath  string
	timeout time.Duration
}

// NewRootCmd builds the command tree. Commands are organized subject first,
// so that every verb is nested under the thing it acts on.
func NewRootCmd(version string) *cobra.Command {
	a := &app{}

	root := &cobra.Command{
		Use:     "gws-cache",
		Short:   "Keep a fast local copy of your email and calendar",
		Long:    "gws-cache keeps a local copy of your Gmail threads and Google Calendar events so you can search and export them without waiting on the network.",
		Version: version,
		// A bad flag or unknown command is a usage problem; a failure inside a
		// command is not, and printing the whole help screen would bury it.
		SilenceUsage: true,
	}

	root.SetVersionTemplate("{{.Version}}\n")

	root.PersistentFlags().StringVar(&a.dbPath, "db", store.DefaultDBPath(), "Path to the local cache file")
	root.PersistentFlags().Lookup("db").DefValue = displayPath(a.dbPath)
	root.PersistentFlags().DurationVar(&a.timeout, "timeout", defaultTimeout, "How long to wait for a single Google Workspace request")

	root.AddCommand(
		newCacheCmd(a),
		newEventCmd(a),
		newMessageCmd(a),
		newTopicCmd(a),
	)

	// Renders aligned tree help in human mode and compact help under AGENT=1.
	cobrahelptree.Setup(root)

	return root
}

// openStore opens the cache database.
func (a *app) openStore() (*store.DB, error) {
	path, err := expandPath(a.dbPath)
	if err != nil {
		return nil, err
	}
	return store.Open(path)
}

// client returns the Google Workspace CLI client.
func (a *app) client() *gws.Client {
	return gws.New()
}

// rules loads the classification rules, including any the user has customized.
func (a *app) rules() (*classifier.Classifier, error) {
	rules, err := classifier.Load(classifier.RulesPath())
	if err != nil {
		return nil, fmt.Errorf("loading classifier rules: %w", err)
	}
	return rules, nil
}

// requestContext derives the per-command deadline from --timeout, leaving the
// context cancellable by the signal handler installed in main.
func (a *app) requestContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	if a.timeout <= 0 {
		return context.WithCancel(cmd.Context())
	}
	return context.WithTimeout(cmd.Context(), a.timeout)
}

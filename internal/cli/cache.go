package cli

import (
	"strconv"

	"github.com/spf13/cobra"
)

// itoa keeps the record rows readable at the call site.
func itoa(n int) string { return strconv.Itoa(n) }

// newCacheCmd builds the "cache" subject: the local copy itself.
func newCacheCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Inspect the local copy itself",
	}
	cmd.AddCommand(newCacheStatusCmd(a))
	return cmd
}

func newCacheStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show what the local copy currently holds",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := newOutput(cmd)

			db, err := a.openStore()
			if err != nil {
				return err
			}
			defer db.Close()

			stats, err := db.Stats()
			if err != nil {
				return err
			}

			return out.records([]record{{
				{Key: "location", Value: a.dbPath},
				{Key: "topics", Value: itoa(stats.Topics)},
				{Key: "conversations", Value: itoa(stats.Threads)},
				{Key: "messages", Value: itoa(stats.Messages)},
			}})
		},
	}
}

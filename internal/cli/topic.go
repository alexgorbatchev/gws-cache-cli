package cli

import (
	"github.com/spf13/cobra"

	"github.com/alexgorbatchev/gws-cache-cli/internal/topic"
)

// neverSynced is shown for a topic that has not been fetched yet.
const neverSynced = "never"

// newTopicCmd builds the "topic" subject. A topic is a saved search and
// nothing more: fetching the mail it stands for lives under "message sync",
// so that defining what to track stays separate from collecting it.
func newTopicCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "topic",
		Short: "Name the searches you want kept up to date",
	}

	cmd.AddCommand(
		newTopicAddCmd(a),
		newTopicListCmd(a),
		newTopicRemoveCmd(a),
	)
	return cmd
}

func newTopicAddCmd(a *app) *cobra.Command {
	var name, query string

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Start tracking a new topic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := newOutput(cmd)

			db, err := a.openStore()
			if err != nil {
				return err
			}
			defer db.Close()

			created, err := topic.NewService(db).RegisterTopic(args[0], name, query)
			if err != nil {
				return err
			}

			out.ok("Now tracking %q (%s)", created.Slug, created.DisplayName)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Friendly name to show in listings")
	cmd.Flags().StringVar(&query, "query", "", "Email search to collect for this topic (for example 'category:promotions')")
	return cmd
}

func newTopicListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show the topics you are tracking",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := newOutput(cmd)

			db, err := a.openStore()
			if err != nil {
				return err
			}
			defer db.Close()

			topics, err := topic.NewService(db).ListTopics()
			if err != nil {
				return err
			}
			if len(topics) == 0 {
				out.printf("No topics tracked yet.")
				return nil
			}

			rows := make([]record, 0, len(topics))
			for _, t := range topics {
				lastSynced := neverSynced
				if t.LastSyncedAt != nil {
					lastSynced = out.formatTime(*t.LastSyncedAt, "2006-01-02 15:04")
				}
				rows = append(rows, record{
					{Key: "slug", Value: t.Slug},
					{Key: "name", Value: t.DisplayName},
					{Key: "search", Value: t.Query},
					{Key: "last_synced", Value: lastSynced},
				})
			}
			return out.records(rows)
		},
	}
}

func newTopicRemoveCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove"},
		Short:   "Stop tracking a topic and forget its email",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := newOutput(cmd)

			db, err := a.openStore()
			if err != nil {
				return err
			}
			defer db.Close()

			if err := topic.NewService(db).DeleteTopic(args[0]); err != nil {
				return err
			}

			out.ok("Stopped tracking %q", args[0])
			return nil
		},
	}
}

package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/alexgorbatchev/gws-cache-cli/internal/exporter"
	"github.com/alexgorbatchev/gws-cache-cli/internal/mailsync"
	"github.com/alexgorbatchev/gws-cache-cli/internal/timewindow"
)

// Default windows for the message subject.
const (
	defaultSearchWindow = "14d"
	defaultSyncWindow   = "4w"
	defaultExportWindow = "4w"
)

// Output styles shared by the commands that can emit JSON.
const (
	formatTable = "table"
	formatJSON  = "json"
)

// newMessageCmd builds the "message" subject: email, both fetching it and
// reading it back.
func newMessageCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "message",
		Short: "Fetch and read your email",
	}
	cmd.AddCommand(
		newMessageSyncCmd(a),
		newMessageSearchCmd(a),
		newMessageExportCmd(a),
	)
	return cmd
}

func newMessageSyncCmd(a *app) *cobra.Command {
	var (
		syncAll    bool
		forceFull  bool
		query      string
		since      string
		maxThreads int
		format     string
	)

	cmd := &cobra.Command{
		Use:   "sync [topic]",
		Short: "Fetch new email into the local copy",
		Long: "Fetch new email into the local copy.\n\n" +
			"Name a topic to sync the search it stands for, pass --all for every topic, " +
			"or pass --query to fetch a one-off search without tracking it.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			targets := 0
			for _, chosen := range []bool{len(args) > 0, syncAll, query != ""} {
				if chosen {
					targets++
				}
			}
			switch targets {
			case 0:
				return fmt.Errorf("name a topic to sync, or pass --all for every topic, or --query for a one-off search")
			case 1:
			default:
				return fmt.Errorf("choose one of a topic name, --all or --query")
			}

			out := newOutput(cmd)

			window, err := timewindow.Parse(since)
			if err != nil {
				return err
			}

			db, err := a.openStore()
			if err != nil {
				return err
			}
			defer db.Close()

			rules, err := a.rules()
			if err != nil {
				return err
			}

			engine := mailsync.NewEngine(db, a.client(), rules)
			engine.Progress = out.progress

			opts := mailsync.Options{
				ForceFull:     forceFull,
				Since:         window,
				SinceExplicit: cmd.Flags().Changed("since"),
				MaxThreads:    maxThreads,
			}

			ctx, cancel := a.requestContext(cmd)
			defer cancel()

			switch {
			case syncAll:
				results, err := engine.SyncAllTopics(ctx, opts)
				if err != nil {
					return err
				}
				if format == formatJSON {
					// Always an array here, even for one topic, so a caller
					// does not have to branch on how many topics exist.
					return out.emitJSON(results)
				}
				if len(results) == 0 {
					out.printf("No topics tracked yet.")
					return nil
				}
				for _, r := range results {
					if r.Error != "" {
						out.failure("%s: %s", r.Target, r.Error)
						continue
					}
					reportSync(out, r)
				}
				return nil

			case query != "":
				res, err := engine.SyncQuery(ctx, query, opts)
				if err != nil {
					return err
				}
				return reportSyncAs(out, format, *res)

			default:
				res, err := engine.SyncTopic(ctx, args[0], opts)
				if err != nil {
					return err
				}
				return reportSyncAs(out, format, *res)
			}
		},
	}

	cmd.Flags().BoolVar(&syncAll, "all", false, "Sync every topic you track")
	cmd.Flags().StringVar(&query, "query", "", "Fetch a one-off search instead of a topic (for example 'is:unread')")
	cmd.Flags().BoolVar(&forceFull, "force-full", false, "Fetch everything again instead of only what changed")
	cmd.Flags().StringVar(&since, "since", defaultSyncWindow, "How far back to look: a span like 4w or 30d, a date like 2026-06-01, or all")
	cmd.Flags().IntVar(&maxThreads, "max-threads", mailsync.DefaultMaxThreads, "New conversations to download in one run; any left over are reported and collected next run")
	cmd.Flags().StringVar(&format, "format", formatTable, "Output style: table or json")
	return cmd
}

// reportSyncAs emits one sync outcome in the requested style. JSON output is
// emitted alone: a status tag mixed into the stream would not parse, and
// threads_remaining already carries the same signal.
func reportSyncAs(out *output, format string, res mailsync.Result) error {
	if format == formatJSON {
		return out.emitJSON(res)
	}
	reportSync(out, res)
	return nil
}

// reportSync prints one sync outcome, leading with the shortfall when the run
// did not finish. A caller that only reads the success line must not be able to
// mistake a partial sync for a complete one.
func reportSync(out *output, res mailsync.Result) {
	if !res.Complete() {
		out.warn("%s: %d of %d new conversations fetched; %d remaining, run again or raise --max-threads",
			res.Target,
			res.ThreadsFetched,
			res.ThreadsFetched+res.ThreadsRemaining,
			res.ThreadsRemaining,
		)
	}
	out.ok("%s: %d conversations, %d messages", res.Target, res.ThreadsFetched, res.MessagesIngested)
}

func newMessageSearchCmd(a *app) *cobra.Command {
	var (
		topicSlug string
		since     string
		humanOnly bool
		format    string
	)

	cmd := &cobra.Command{
		Use:   "search [words]",
		Short: "Search saved email without going online",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := newOutput(cmd)

			window, err := timewindow.Parse(since)
			if err != nil {
				return err
			}

			db, err := a.openStore()
			if err != nil {
				return err
			}
			defer db.Close()

			keyword := ""
			if len(args) > 0 {
				keyword = args[0]
			}

			// An unbounded window yields zero, which the store reads as no date filter.
			sinceSec, _ := window.StartUnix(time.Now())
			messages, err := db.SearchMessages(keyword, topicSlug, humanOnly, sinceSec)
			if err != nil {
				return err
			}

			if len(messages) == 0 {
				out.printf("No saved email matches that search.")
				return nil
			}

			if format == formatJSON {
				return out.emitJSON(messages)
			}

			rows := make([]record, 0, len(messages))
			for _, m := range messages {
				sender := m.FromName
				if sender == "" {
					sender = m.FromAddress
				}
				rows = append(rows, record{
					{Key: "date", Value: out.formatDate(m.DateISO)},
					{Key: "from", Value: sender},
					{Key: "subject", Value: m.Subject},
					{Key: "snippet", Value: m.Snippet},
				})
			}
			return out.records(rows)
		},
	}

	cmd.Flags().StringVar(&topicSlug, "topic", "", "Only look inside this topic")
	cmd.Flags().StringVar(&since, "since", defaultSearchWindow, "How far back to search: a span like 14d, a date like 2026-06-01, or all")
	cmd.Flags().BoolVar(&humanOnly, "human-only", false, "Leave out automatic notifications and receipts")
	cmd.Flags().StringVar(&format, "format", formatTable, "Output style: table or json")
	return cmd
}

func newMessageExportCmd(a *app) *cobra.Command {
	var (
		humanOnly bool
		format    string
		since     string
	)

	cmd := &cobra.Command{
		Use:   "export <topic>",
		Short: "Write a topic's email out as a timeline",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := newOutput(cmd)

			window, err := timewindow.Parse(since)
			if err != nil {
				return err
			}

			db, err := a.openStore()
			if err != nil {
				return err
			}
			defer db.Close()

			timeline, err := exporter.NewExporter(db).ExportTopic(args[0], exporter.Options{
				HumanOnly: humanOnly,
				Format:    exporter.Format(format),
				Since:     window,
			})
			if err != nil {
				return err
			}

			out.printf("%s", timeline)
			return nil
		},
	}

	cmd.Flags().BoolVar(&humanOnly, "human-only", true, "Leave out automatic notifications and receipts")
	cmd.Flags().StringVar(&format, "format", string(exporter.FormatMarkdown), "Output style: markdown or json")
	cmd.Flags().StringVar(&since, "since", defaultExportWindow, "How far back to include: a span like 4w, a date like 2026-06-01, or all")
	return cmd
}

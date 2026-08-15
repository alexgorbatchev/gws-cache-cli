package main

import (
	"encoding/json"
	"os"

	"github.com/spf13/cobra"

	"gws-cache/pkg/calendar"
	"gws-cache/pkg/exporter"
	"gws-cache/pkg/gmail"
	"gws-cache/pkg/scan"
	"gws-cache/pkg/store"
	"gws-cache/pkg/sync"
	"gws-cache/pkg/topic"
)

var (
	dbPath string

	newClient = func() gmail.Client {
		return gmail.NewCLIClient()
	}
)

func NewRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "gws-cache",
		Short: "Local SQLite caching layer for Gmail threads and Google Calendar via gws CLI",
	}

	defaultDB := store.DefaultDBPath()
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", defaultDB, "Path to SQLite cache database")

	rootCmd.AddCommand(
		newTopicCmd(),
		newSyncCmd(),
		newCalendarCmd(),
		newScanCmd(),
		newSearchCmd(),
		newExportCmd(),
		newStatusCmd(),
	)

	return rootCmd
}

func getDB() (*store.DB, error) {
	if dbPath == "" {
		dbPath = store.DefaultDBPath()
	}
	return store.Open(dbPath)
}

func newTopicCmd() *cobra.Command {
	topicCmd := &cobra.Command{
		Use:   "topic",
		Short: "Manage tracked email topics and search queries",
	}

	var name, query string
	addCmd := &cobra.Command{
		Use:   "add <slug>",
		Short: "Track a new topic or feed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := getDB()
			if err != nil {
				return err
			}
			defer db.Close()

			svc := topic.NewService(db)
			slug := args[0]
			t, err := svc.RegisterTopic(slug, name, query)
			if err != nil {
				return err
			}

			cmd.Printf("Registered topic %q (%s)\n", t.Slug, t.DisplayName)
			return nil
		},
	}
	addCmd.Flags().StringVar(&name, "name", "", "Display name for the topic")
	addCmd.Flags().StringVar(&query, "query", "", "Gmail search query (e.g. 'category:promotions')")

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List tracked topics",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := getDB()
			if err != nil {
				return err
			}
			defer db.Close()

			svc := topic.NewService(db)
			topics, err := svc.ListTopics()
			if err != nil {
				return err
			}

			if len(topics) == 0 {
				cmd.Println("No topics tracked yet.")
				return nil
			}

			cmd.Printf("%-15s %-25s %-25s %-20s\n", "SLUG", "DISPLAY NAME", "QUERY", "LAST SYNCED")
			for _, t := range topics {
				lastSync := "Never"
				if t.LastSyncedAt != nil {
					lastSync = t.LastSyncedAt.Format("2006-01-02 15:04")
				}
				cmd.Printf("%-15s %-25s %-25s %-20s\n", t.Slug, truncate(t.DisplayName, 24), truncate(t.Query, 24), lastSync)
			}
			return nil
		},
	}

	removeCmd := &cobra.Command{
		Use:   "remove <slug>",
		Short: "Stop tracking a topic",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := getDB()
			if err != nil {
				return err
			}
			defer db.Close()

			svc := topic.NewService(db)
			if err := svc.DeleteTopic(args[0]); err != nil {
				return err
			}
			cmd.Printf("Removed topic %q\n", args[0])
			return nil
		},
	}

	topicCmd.AddCommand(addCmd, listCmd, removeCmd)
	return topicCmd
}

func newSyncCmd() *cobra.Command {
	var syncAll bool
	var forceFull bool
	var since string
	var maxThreads int

	cmd := &cobra.Command{
		Use:   "sync [slug]",
		Short: "Synchronize email threads from Gmail into SQLite cache",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := getDB()
			if err != nil {
				return err
			}
			defer db.Close()

			client := newClient()
			engine := sync.NewEngine(db, client)
			engine.Progress = func(format string, a ...any) {
				cmd.PrintErrf("[gws-cache] "+format+"\n", a...)
			}

			opts := sync.SyncOptions{
				ForceFull:  forceFull,
				Since:      since,
				MaxThreads: maxThreads,
			}

			if syncAll || len(args) == 0 {
				cmd.Println("Synchronizing all tracked topics...")
				results, err := engine.SyncAllTopics(opts)
				if err != nil {
					return err
				}
				for _, r := range results {
					if r.Error != "" {
						cmd.Printf("  - %s: error (%s)\n", r.TopicSlug, r.Error)
					} else {
						cmd.Printf("  - %s: %d threads, %d messages\n", r.TopicSlug, r.ThreadsFetched, r.MessagesIngested)
					}
				}
				return nil
			}

			slug := args[0]
			res, err := engine.SyncTopic(slug, opts)
			if err != nil {
				return err
			}
			cmd.Printf("Done: Synced %s (%d threads, %d messages).\n", res.TopicSlug, res.ThreadsFetched, res.MessagesIngested)
			return nil
		},
	}

	cmd.Flags().BoolVar(&syncAll, "all", false, "Sync all tracked topics")
	cmd.Flags().BoolVar(&forceFull, "force-full", false, "Force full re-fetch of all threads")
	cmd.Flags().StringVar(&since, "since", "4w", "Lookback duration window (e.g. 4w, 30d, 2m, 2026-06-01, all)")
	cmd.Flags().IntVar(&maxThreads, "max-threads", 25, "Maximum number of threads to fetch per sync")
	return cmd
}

func newCalendarCmd() *cobra.Command {
	calCmd := &cobra.Command{
		Use:   "calendar",
		Short: "Synchronize and inspect Google Calendar events",
	}

	var calendarID, past, future string
	var forceFull bool

	syncCmd := &cobra.Command{
		Use:   "sync",
		Short: "Synchronize Google Calendar events with syncToken delta ingestion",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := getDB()
			if err != nil {
				return err
			}
			defer db.Close()

			client := newClient()
			engine := calendar.NewCalendarEngine(db, client)
			engine.Progress = func(format string, a ...any) {
				cmd.PrintErrf("[gws-cache] "+format+"\n", a...)
			}

			res, err := engine.SyncCalendar(calendar.SyncOptions{
				CalendarID: calendarID,
				Past:       past,
				Future:     future,
				ForceFull:  forceFull,
			})
			if err != nil {
				return err
			}

			cmd.Printf("Calendar sync complete: %d events fetched (%d cancelled/deleted).\n", res.EventsFetched, res.EventsDeleted)
			return nil
		},
	}

	syncCmd.Flags().StringVar(&calendarID, "calendar-id", "primary", "Google Calendar ID")
	syncCmd.Flags().StringVar(&past, "past", "4w", "Past lookback window (e.g. 4w, 30d, 2m, 2026-06-01, all)")
	syncCmd.Flags().StringVar(&future, "future", "4w", "Future lookahead window (e.g. 4w, 30d, 2m, 2026-09-01, all)")
	syncCmd.Flags().BoolVar(&forceFull, "force-full", false, "Force full window re-sync (bypassing syncToken)")

	var format string
	var since string

	listCmd := &cobra.Command{
		Use:   "list [topic_slug]",
		Short: "List cached Google Calendar events",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := getDB()
			if err != nil {
				return err
			}
			defer db.Close()

			topicSlug := ""
			if len(args) > 0 {
				topicSlug = args[0]
			}

			sinceSec, _ := sync.ParseSince(since)
			events, err := db.ListCalendarEvents(calendarID, topicSlug, sinceSec)
			if err != nil {
				return err
			}

			if len(events) == 0 {
				cmd.Println("No calendar events found.")
				return nil
			}

			if format == "json" {
				b, _ := json.MarshalIndent(events, "", "  ")
				cmd.Println(string(b))
				return nil
			}

			cmd.Printf("%-12s %-12s %-30s %-12s %s\n", "DATE", "TIME", "SUMMARY", "STATUS", "LOCATION")
			for _, e := range events {
				statusStr := e.Status
				if e.IsDeleted {
					statusStr = "CANCELLED"
				}
				cmd.Printf("%-12s %-12s %-30s %-12s %s\n",
					e.StartTime.Format("Jan 02, 2006"),
					e.StartTime.Format("15:04"),
					truncate(e.Summary, 29),
					statusStr,
					e.Location,
				)
			}
			return nil
		},
	}

	listCmd.Flags().StringVar(&calendarID, "calendar-id", "primary", "Google Calendar ID")
	listCmd.Flags().StringVar(&since, "since", "4w", "Filter calendar events by lookback date")
	listCmd.Flags().StringVar(&format, "format", "table", "Output format: table or json")

	calCmd.AddCommand(syncCmd, listCmd)
	return calCmd
}

func newScanCmd() *cobra.Command {
	var query string
	var since string
	var maxThreads int

	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan inbox for emails matching a query and cache them in SQLite",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := getDB()
			if err != nil {
				return err
			}
			defer db.Close()

			client := newClient()
			scanner := scan.NewScanner(db, client)
			scanner.Progress = func(format string, a ...any) {
				cmd.PrintErrf("[gws-cache] "+format+"\n", a...)
			}

			res, err := scanner.ScanInbox(scan.ScanOptions{
				Query:      query,
				Since:      since,
				MaxThreads: maxThreads,
			})
			if err != nil {
				return err
			}

			cmd.Printf("Scan complete: %d evaluated, %d skipped, %d msgs ingested.\n", res.ThreadsEvaluated, res.ThreadsSkipped, res.MessagesIngested)
			return nil
		},
	}

	cmd.Flags().StringVar(&query, "query", "in:inbox", "Gmail search query for scan")
	cmd.Flags().StringVar(&since, "since", "7d", "Lookback duration window (e.g. 7d, 2w, 1m)")
	cmd.Flags().IntVar(&maxThreads, "max-threads", 50, "Maximum number of candidate threads to evaluate")
	return cmd
}

func newSearchCmd() *cobra.Command {
	var topicSlug string
	var since string
	var humanOnly bool
	var format string

	cmd := &cobra.Command{
		Use:   "search [keyword]",
		Short: "Search cached emails in local SQLite",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := getDB()
			if err != nil {
				return err
			}
			defer db.Close()

			client := newClient()
			scanner := scan.NewScanner(db, client)

			keyword := ""
			if len(args) > 0 {
				keyword = args[0]
			}

			msgs, err := scanner.SearchCached(scan.SearchOptions{
				Keyword:   keyword,
				TopicSlug: topicSlug,
				HumanOnly: humanOnly,
				Since:     since,
			})
			if err != nil {
				return err
			}

			if len(msgs) == 0 {
				cmd.Println("No matching cached messages found.")
				return nil
			}

			if format == "json" {
				b, _ := json.MarshalIndent(msgs, "", "  ")
				cmd.Println(string(b))
				return nil
			}

			cmd.Printf("%-25s %-35s %-12s %s\n", "SENDER", "SUBJECT", "DATE", "SNIPPET")
			for _, m := range msgs {
				sender := m.FromName
				if sender == "" {
					sender = m.FromAddress
				}
				cmd.Printf("%-25s %-35s %-12s %s\n", truncate(sender, 24), truncate(m.Subject, 34), m.DateISO[:10], truncate(m.Snippet, 40))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&topicSlug, "topic", "", "Filter by topic slug")
	cmd.Flags().StringVar(&since, "since", "14d", "Filter emails by date (e.g. 14d, 1m, all)")
	cmd.Flags().BoolVar(&humanOnly, "human-only", false, "Filter out automated emails")
	cmd.Flags().StringVar(&format, "format", "table", "Output format: table or json")
	return cmd
}

func newExportCmd() *cobra.Command {
	var humanOnly bool
	var format string
	var since string

	cmd := &cobra.Command{
		Use:   "export <slug>",
		Short: "Export topic timeline in JSON or Markdown format",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := getDB()
			if err != nil {
				return err
			}
			defer db.Close()

			exp := exporter.NewExporter(db)
			slug := args[0]
			out, err := exp.ExportTopic(slug, exporter.Options{
				HumanOnly: humanOnly,
				Format:    format,
				Since:     since,
			})
			if err != nil {
				return err
			}

			cmd.Println(out)
			return nil
		},
	}

	cmd.Flags().BoolVar(&humanOnly, "human-only", true, "Exclude automated system emails")
	cmd.Flags().StringVar(&format, "format", "markdown", "Export format: markdown or json")
	cmd.Flags().StringVar(&since, "since", "4w", "Filter export by lookback duration window (e.g. 4w, 30d, 1m, 2026-06-01, all)")
	return cmd
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show database statistics and sync state",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := getDB()
			if err != nil {
				return err
			}
			defer db.Close()

			topics, threads, messages, err := db.Stats()
			if err != nil {
				return err
			}

			cmd.Printf("Database Stats (%s):\n", dbPath)
			cmd.Printf("  Tracked Topics:  %d\n", topics)
			cmd.Printf("  Cached Threads:  %d\n", threads)
			cmd.Printf("  Cached Messages: %d\n", messages)
			return nil
		},
	}
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

func main() {
	if err := NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

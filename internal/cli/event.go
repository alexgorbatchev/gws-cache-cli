package cli

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/alexgorbatchev/gws-cache-cli/internal/event"
	"github.com/alexgorbatchev/gws-cache-cli/internal/timewindow"
)

// Default windows for the event subject.
const (
	defaultEventPast   = "4w"
	defaultEventFuture = "4w"
	defaultEventSince  = "4w"

	eventDateLayout = "Jan 02, 2006"
	eventTimeLayout = "15:04"

	statusCancelledLabel = "cancelled"
)

// newEventCmd builds the "event" subject: calendar entries, both fetching them
// and reading them back. It mirrors "message" so the two content types behave
// the same way.
func newEventCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "event",
		Short: "Fetch and read your calendar entries",
	}
	cmd.AddCommand(newEventSyncCmd(a), newEventSearchCmd(a))
	return cmd
}

func newEventSyncCmd(a *app) *cobra.Command {
	var (
		calendarID string
		past       string
		future     string
		forceFull  bool
		format     string
	)

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch calendar entries into the local copy",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := newOutput(cmd)

			pastWindow, err := timewindow.Parse(past)
			if err != nil {
				return err
			}
			futureWindow, err := timewindow.Parse(future)
			if err != nil {
				return err
			}

			db, err := a.openStore()
			if err != nil {
				return err
			}
			defer db.Close()

			engine := event.NewEngine(db, a.client())
			engine.Progress = out.progress

			ctx, cancel := a.requestContext(cmd)
			defer cancel()

			res, err := engine.Sync(ctx, event.SyncOptions{
				CalendarID: calendarID,
				Past:       pastWindow,
				Future:     futureWindow,
				ForceFull:  forceFull,
			})
			if err != nil {
				return err
			}

			if format == formatJSON {
				return out.emitJSON(res)
			}

			out.ok("%s: %d entries saved (%d cancelled)", res.CalendarID, res.EventsIngested, res.EventsDeleted)
			return nil
		},
	}

	cmd.Flags().StringVar(&calendarID, "calendar-id", event.DefaultCalendarID, "Which calendar to copy")
	cmd.Flags().StringVar(&past, "past", defaultEventPast, "How far back to include: a span like 4w, a date like 2026-06-01, or all")
	cmd.Flags().StringVar(&future, "future", defaultEventFuture, "How far ahead to include: a span like 4w, a date like 2026-12-01, or all")
	cmd.Flags().BoolVar(&forceFull, "force-full", false, "Fetch the whole window again instead of only what changed")
	cmd.Flags().StringVar(&format, "format", formatTable, "Output style: table or json")
	return cmd
}

func newEventSearchCmd(a *app) *cobra.Command {
	var (
		calendarID string
		topicSlug  string
		since      string
		format     string
	)

	cmd := &cobra.Command{
		Use:   "search [words]",
		Short: "Search saved calendar entries without going online",
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

			// An unbounded window yields zero, read as no date filter.
			sinceSec, _ := window.StartUnix(time.Now())
			events, err := db.SearchCalendarEvents(keyword, calendarID, topicSlug, sinceSec)
			if err != nil {
				return err
			}

			if len(events) == 0 {
				out.printf("No saved calendar entries match that search.")
				return nil
			}

			if format == formatJSON {
				return out.emitJSON(events)
			}

			rows := make([]record, 0, len(events))
			for _, e := range events {
				status := e.Status
				if e.IsDeleted {
					status = statusCancelledLabel
				}
				rows = append(rows, record{
					{Key: "date", Value: out.formatTime(e.StartTime, eventDateLayout)},
					{Key: "time", Value: e.StartTime.Format(eventTimeLayout)},
					{Key: "summary", Value: e.Summary},
					{Key: "status", Value: status},
					{Key: "location", Value: e.Location},
				})
			}
			return out.records(rows)
		},
	}

	cmd.Flags().StringVar(&calendarID, "calendar-id", event.DefaultCalendarID, "Which calendar to read")
	cmd.Flags().StringVar(&topicSlug, "topic", "", "Only show entries matched to this topic")
	cmd.Flags().StringVar(&since, "since", defaultEventSince, "How far back to include: a span like 4w, a date like 2026-06-01, or all")
	cmd.Flags().StringVar(&format, "format", formatTable, "Output style: table or json")
	return cmd
}

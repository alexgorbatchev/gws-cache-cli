package event

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/gws-cache-cli/internal/gws"
	"github.com/alexgorbatchev/gws-cache-cli/internal/store"
	"github.com/alexgorbatchev/gws-cache-cli/internal/timewindow"
)

// fakeLister stands in for the gws client. The calendar engine declares the
// narrow Lister interface it needs, so the fake implements only that.
type fakeLister struct {
	responses []*gws.CalendarEventsResponse
	errs      []error
	calls     []gws.CalendarListParams
}

func (f *fakeLister) ListCalendarEvents(_ context.Context, p gws.CalendarListParams) (*gws.CalendarEventsResponse, error) {
	i := len(f.calls)
	f.calls = append(f.calls, p)
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	if i < len(f.responses) {
		return f.responses[i], nil
	}
	return &gws.CalendarEventsResponse{}, nil
}

func newTestStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "cal.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func window(t *testing.T, expr string) timewindow.Window {
	t.Helper()
	w, err := timewindow.Parse(expr)
	if err != nil {
		t.Fatalf("parsing window %q: %v", expr, err)
	}
	return w
}

func sampleEvents() *gws.CalendarEventsResponse {
	timed := gws.CalendarEventDetail{
		ID:          "evt-1",
		Status:      "confirmed",
		Summary:     "Call with newsletters",
		Description: "Editorial sync",
	}
	timed.Start.DateTime = "2026-07-29T11:00:00Z"
	timed.End.DateTime = "2026-07-29T12:00:00Z"
	timed.Organizer.Email = "editor@newsletter.example"
	timed.Attendees = []gws.CalendarAttendee{{Email: "editor@newsletter.example", DisplayName: "Editor"}}

	allDay := gws.CalendarEventDetail{ID: "evt-2", Status: statusCancelled}
	allDay.Start.Date = "2026-07-27"
	allDay.End.Date = "2026-07-27"

	return &gws.CalendarEventsResponse{
		NextSyncToken: "sync-100",
		Items:         []gws.CalendarEventDetail{timed, allDay},
	}
}

func TestSyncIngestsEvents(t *testing.T) {
	db := newTestStore(t)
	if _, err := db.CreateTopic("newsletters", "Tech Newsletters", "q"); err != nil {
		t.Fatalf("creating topic: %v", err)
	}

	client := &fakeLister{responses: []*gws.CalendarEventsResponse{sampleEvents()}}
	res, err := NewEngine(db, client).Sync(t.Context(), SyncOptions{
		Past:   window(t, "4w"),
		Future: window(t, "4w"),
	})
	if err != nil {
		t.Fatalf("Sync unexpected error: %v", err)
	}

	if res.EventsFetched != 2 || res.EventsIngested != 2 || res.EventsDeleted != 1 {
		t.Fatalf("Sync result = %+v", res)
	}
	if res.CalendarID != DefaultCalendarID {
		t.Fatalf("CalendarID = %q, want %q", res.CalendarID, DefaultCalendarID)
	}

	events, err := db.SearchCalendarEvents("", DefaultCalendarID, "newsletters", 0)
	if err != nil {
		t.Fatalf("listing events: %v", err)
	}
	if len(events) != 1 || events[0].ID != "evt-1" {
		t.Fatalf("expected evt-1 matched to the newsletters topic, got %+v", events)
	}
}

func TestSyncUnboundedWindowsSendNoBounds(t *testing.T) {
	// "all" must reach Google as an unbounded request rather than silently
	// falling back to the default four week window.
	client := &fakeLister{responses: []*gws.CalendarEventsResponse{{}}}

	if _, err := NewEngine(newTestStore(t), client).Sync(t.Context(), SyncOptions{
		Past:   window(t, "all"),
		Future: window(t, "all"),
	}); err != nil {
		t.Fatalf("Sync unexpected error: %v", err)
	}

	if len(client.calls) != 1 {
		t.Fatalf("expected one request, got %d", len(client.calls))
	}
	if got := client.calls[0]; got.TimeMin != "" || got.TimeMax != "" {
		t.Fatalf("unbounded windows should send no time bounds, got TimeMin=%q TimeMax=%q", got.TimeMin, got.TimeMax)
	}
}

func TestSyncAppliesRequestedWindow(t *testing.T) {
	client := &fakeLister{responses: []*gws.CalendarEventsResponse{{}}}

	if _, err := NewEngine(newTestStore(t), client).Sync(t.Context(), SyncOptions{
		Past:   window(t, "2026-01-01"),
		Future: window(t, "2026-12-31"),
	}); err != nil {
		t.Fatalf("Sync unexpected error: %v", err)
	}

	got := client.calls[0]
	if got.TimeMin != "2026-01-01T00:00:00Z" {
		t.Fatalf("TimeMin = %q, want the requested start date", got.TimeMin)
	}
	if got.TimeMax != "2026-12-31T00:00:00Z" {
		t.Fatalf("TimeMax = %q, want the requested end date", got.TimeMax)
	}
}

func TestSyncFutureWindowLooksForward(t *testing.T) {
	client := &fakeLister{responses: []*gws.CalendarEventsResponse{{}}}

	before := time.Now().UTC()
	if _, err := NewEngine(newTestStore(t), client).Sync(t.Context(), SyncOptions{
		Past:   window(t, "1w"),
		Future: window(t, "6w"),
	}); err != nil {
		t.Fatalf("Sync unexpected error: %v", err)
	}

	got := client.calls[0]
	timeMax, err := time.Parse(time.RFC3339, got.TimeMax)
	if err != nil {
		t.Fatalf("parsing TimeMax %q: %v", got.TimeMax, err)
	}
	if !timeMax.After(before.Add(5 * 7 * 24 * time.Hour)) {
		t.Fatalf("TimeMax %s should be about six weeks ahead of %s", timeMax, before)
	}
}

func TestSyncUsesStoredSyncToken(t *testing.T) {
	db := newTestStore(t)
	if err := db.UpdateCalendarSyncState(&store.CalendarSyncState{CalendarID: DefaultCalendarID, SyncToken: "tok-1"}); err != nil {
		t.Fatalf("seeding sync state: %v", err)
	}

	client := &fakeLister{responses: []*gws.CalendarEventsResponse{{NextSyncToken: "tok-2"}}}
	if _, err := NewEngine(db, client).Sync(t.Context(), SyncOptions{Past: window(t, "4w"), Future: window(t, "4w")}); err != nil {
		t.Fatalf("Sync unexpected error: %v", err)
	}

	if client.calls[0].SyncToken != "tok-1" {
		t.Fatalf("expected the stored sync token to be used, got %q", client.calls[0].SyncToken)
	}

	state, err := db.GetCalendarSyncState(DefaultCalendarID)
	if err != nil || state == nil {
		t.Fatalf("reading sync state: %v", err)
	}
	if state.SyncToken != "tok-2" {
		t.Fatalf("sync token = %q, want the token returned by the last sync", state.SyncToken)
	}
}

func TestSyncFallsBackWhenSyncTokenRejected(t *testing.T) {
	db := newTestStore(t)
	if err := db.UpdateCalendarSyncState(&store.CalendarSyncState{CalendarID: DefaultCalendarID, SyncToken: "stale"}); err != nil {
		t.Fatalf("seeding sync state: %v", err)
	}

	client := &fakeLister{
		errs:      []error{errors.New("sync token expired")},
		responses: []*gws.CalendarEventsResponse{nil, sampleEvents()},
	}

	res, err := NewEngine(db, client).Sync(t.Context(), SyncOptions{Past: window(t, "4w"), Future: window(t, "4w")})
	if err != nil {
		t.Fatalf("Sync unexpected error: %v", err)
	}
	if res.EventsIngested != 2 {
		t.Fatalf("expected the windowed retry to ingest events, got %+v", res)
	}
	if len(client.calls) != 2 {
		t.Fatalf("expected a retry, got %d calls", len(client.calls))
	}
	if client.calls[1].SyncToken != "" || client.calls[1].TimeMin == "" {
		t.Fatalf("retry should be a windowed request, got %+v", client.calls[1])
	}
}

func TestSyncKeepsPreviousTokenWhenNoneReturned(t *testing.T) {
	db := newTestStore(t)
	if err := db.UpdateCalendarSyncState(&store.CalendarSyncState{CalendarID: DefaultCalendarID, SyncToken: "tok-1"}); err != nil {
		t.Fatalf("seeding sync state: %v", err)
	}

	client := &fakeLister{responses: []*gws.CalendarEventsResponse{{}}}
	if _, err := NewEngine(db, client).Sync(t.Context(), SyncOptions{Past: window(t, "4w"), Future: window(t, "4w")}); err != nil {
		t.Fatalf("Sync unexpected error: %v", err)
	}

	state, _ := db.GetCalendarSyncState(DefaultCalendarID)
	if state == nil || state.SyncToken != "tok-1" {
		t.Fatalf("expected the previous token to be kept, got %+v", state)
	}
}

func TestSyncReportsListFailure(t *testing.T) {
	client := &fakeLister{errs: []error{errors.New("network down")}}

	if _, err := NewEngine(newTestStore(t), client).Sync(t.Context(), SyncOptions{Past: window(t, "4w"), Future: window(t, "4w")}); err == nil {
		t.Fatal("Sync should report a listing failure")
	}
}

func TestSyncStopsOnCancelledContext(t *testing.T) {
	db := newTestStore(t)
	if err := db.UpdateCalendarSyncState(&store.CalendarSyncState{CalendarID: DefaultCalendarID, SyncToken: "tok"}); err != nil {
		t.Fatalf("seeding sync state: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	client := &fakeLister{errs: []error{context.Canceled}}
	if _, err := NewEngine(db, client).Sync(ctx, SyncOptions{Past: window(t, "4w"), Future: window(t, "4w")}); err == nil {
		t.Fatal("a cancelled context should not trigger a windowed retry")
	}
	if len(client.calls) != 1 {
		t.Fatalf("expected no retry after cancellation, got %d calls", len(client.calls))
	}
}

func TestTopicMatchingIsDeterministic(t *testing.T) {
	db := newTestStore(t)
	for _, slug := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
		if _, err := db.CreateTopic(slug, slug, ""); err != nil {
			t.Fatalf("creating topic %q: %v", slug, err)
		}
	}

	// Every topic name appears in the summary, so only a deterministic
	// ordering can produce the same answer on every run.
	const summary = "alpha beta gamma delta epsilon planning"

	var first *int64
	for i := range 25 {
		matcher, err := NewEngine(db, &fakeLister{}).newTopicMatcher()
		if err != nil {
			t.Fatalf("building matcher: %v", err)
		}
		got := matcher.match(summary, "")
		if got == nil {
			t.Fatal("expected a topic match")
		}
		if i == 0 {
			first = got
			continue
		}
		if *got != *first {
			t.Fatalf("topic match changed between runs: %d then %d", *first, *got)
		}
	}
}

func TestTopicMatchingRequiresWholeWords(t *testing.T) {
	db := newTestStore(t)
	if _, err := db.CreateTopic("ai", "ai", ""); err != nil {
		t.Fatalf("creating topic: %v", err)
	}

	matcher, err := NewEngine(db, &fakeLister{}).newTopicMatcher()
	if err != nil {
		t.Fatalf("building matcher: %v", err)
	}

	if got := matcher.match("Retail airline sync", ""); got != nil {
		t.Fatalf("topic %q should not match inside another word, got topic %d", "ai", *got)
	}
	if got := matcher.match("Weekly AI review", ""); got == nil {
		t.Fatal("topic should match as a whole word regardless of case")
	}
}

func TestTopicMatchingPrefersLongerTerms(t *testing.T) {
	db := newTestStore(t)
	short, err := db.CreateTopic("sync", "sync", "")
	if err != nil {
		t.Fatalf("creating topic: %v", err)
	}
	long, err := db.CreateTopic("calendar-sync", "calendar-sync", "")
	if err != nil {
		t.Fatalf("creating topic: %v", err)
	}

	matcher, err := NewEngine(db, &fakeLister{}).newTopicMatcher()
	if err != nil {
		t.Fatalf("building matcher: %v", err)
	}

	got := matcher.match("calendar-sync planning", "")
	if got == nil {
		t.Fatal("expected a match")
	}
	if *got != long.ID {
		t.Fatalf("matched topic %d, want the longer term %d (short term is %d)", *got, long.ID, short.ID)
	}
}

func TestParseEventTimes(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		start     string
		startDate string
		end       string
		endDate   string
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "timed event",
			start:     "2026-07-29T11:00:00Z",
			end:       "2026-07-29T12:00:00Z",
			wantStart: time.Date(2026, 7, 29, 11, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		},
		{
			name:      "all day event",
			startDate: "2026-07-27",
			endDate:   "2026-07-28",
			wantStart: time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
		},
		{
			name:      "missing times fall back to now plus default duration",
			wantStart: now,
			wantEnd:   now.Add(defaultEventDuration),
		},
		{
			name:      "missing end gets the default duration",
			start:     "2026-07-29T11:00:00Z",
			wantStart: time.Date(2026, 7, 29, 11, 0, 0, 0, time.UTC),
			wantEnd:   time.Date(2026, 7, 29, 11, 30, 0, 0, time.UTC),
		},
		{
			name:      "unparseable times fall back",
			start:     "not-a-time",
			end:       "not-a-time",
			wantStart: now,
			wantEnd:   now.Add(defaultEventDuration),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var item gws.CalendarEventDetail
			item.Start.DateTime, item.Start.Date = tt.start, tt.startDate
			item.End.DateTime, item.End.Date = tt.end, tt.endDate

			gotStart, gotEnd := parseEventTimes(item, now)
			if !gotStart.Equal(tt.wantStart) {
				t.Fatalf("start = %s, want %s", gotStart, tt.wantStart)
			}
			if !gotEnd.Equal(tt.wantEnd) {
				t.Fatalf("end = %s, want %s", gotEnd, tt.wantEnd)
			}
		})
	}
}

func TestDescribeWindow(t *testing.T) {
	tests := []struct {
		name   string
		params gws.CalendarListParams
		want   string
	}{
		{name: "unbounded", params: gws.CalendarListParams{}, want: "the beginning to the end of time"},
		{
			name:   "bounded",
			params: gws.CalendarListParams{TimeMin: "2026-01-01T00:00:00Z", TimeMax: "2026-12-31T00:00:00Z"},
			want:   "2026-01-01 to 2026-12-31",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := describeWindow(tt.params); got != tt.want {
				t.Fatalf("describeWindow = %q, want %q", got, tt.want)
			}
		})
	}
}

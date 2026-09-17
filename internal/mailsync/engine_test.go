package mailsync

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexgorbatchev/gws-cache-cli/internal/classifier"
	"github.com/alexgorbatchev/gws-cache-cli/internal/gws"
	"github.com/alexgorbatchev/gws-cache-cli/internal/store"
	"github.com/alexgorbatchev/gws-cache-cli/internal/timewindow"
)

// fakeFetcher records the queries it is asked for and replays canned threads.
type fakeFetcher struct {
	queries    []string
	threads    []gws.ThreadSummary
	details    map[string]*gws.ThreadDetail
	listErr    error
	detailErr  error
	detailFail map[string]bool
}

func (f *fakeFetcher) ListThreads(_ context.Context, query string) ([]gws.ThreadSummary, error) {
	f.queries = append(f.queries, query)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.threads, nil
}

func (f *fakeFetcher) GetThread(_ context.Context, threadID string) (*gws.ThreadDetail, error) {
	if f.detailErr != nil {
		return nil, f.detailErr
	}
	if f.detailFail[threadID] {
		return nil, errors.New("thread unavailable")
	}
	if detail, ok := f.details[threadID]; ok {
		return detail, nil
	}
	return &gws.ThreadDetail{ID: threadID}, nil
}

func newTestStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "sync.db"))
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

func threadWithMessage(id, historyID, internalDate string) *gws.ThreadDetail {
	return &gws.ThreadDetail{
		ID:        id,
		HistoryID: historyID,
		Messages: []gws.MessageDetail{{
			ID:           id + "-m1",
			ThreadID:     id,
			InternalDate: internalDate,
			Snippet:      "Weekly summary",
			Payload: gws.Payload{Headers: []gws.Header{
				{Name: "From", Value: "Editor <editor@newsletter.example>"},
				{Name: "To", Value: "me@example.com"},
				{Name: "Subject", Value: "Weekly Digest"},
				{Name: "Date", Value: "Mon, 01 Jun 2026 10:00:00 +0000"},
			}},
		}},
	}
}

func newEngine(t *testing.T, db *store.DB, client ThreadFetcher) *Engine {
	t.Helper()
	return NewEngine(db, client, classifier.Default())
}

func TestSyncTopicIngestsThreads(t *testing.T) {
	db := newTestStore(t)
	client := &fakeFetcher{
		threads: []gws.ThreadSummary{{ID: "t-1", HistoryID: "100", Snippet: "Weekly summary"}},
		details: map[string]*gws.ThreadDetail{"t-1": threadWithMessage("t-1", "150", "1780000000000")},
	}

	res, err := newEngine(t, db, client).SyncTopic(t.Context(), "newsletters", Options{Since: window(t, "4w")})
	if err != nil {
		t.Fatalf("SyncTopic unexpected error: %v", err)
	}
	if res.ThreadsFetched != 1 || res.MessagesIngested != 1 {
		t.Fatalf("SyncTopic result = %+v", res)
	}
	if res.MaxHistoryID != "150" {
		t.Fatalf("MaxHistoryID = %q, want the thread detail history id", res.MaxHistoryID)
	}

	top, err := db.GetTopicBySlug("newsletters")
	if err != nil {
		t.Fatalf("topic should have been auto-registered: %v", err)
	}
	if top.LastSyncedAt == nil {
		t.Fatal("sync state was not recorded")
	}
}

func TestSyncTopicSkipsCachedThreads(t *testing.T) {
	db := newTestStore(t)
	top, err := db.CreateTopic("newsletters", "Newsletters", "q")
	if err != nil {
		t.Fatalf("creating topic: %v", err)
	}
	if err := db.UpsertThread("t-1", &top.ID, "100", "cached"); err != nil {
		t.Fatalf("seeding thread: %v", err)
	}

	client := &fakeFetcher{threads: []gws.ThreadSummary{{ID: "t-1", HistoryID: "100"}}}
	res, err := newEngine(t, db, client).SyncTopic(t.Context(), "newsletters", Options{Since: window(t, "4w")})
	if err != nil {
		t.Fatalf("SyncTopic unexpected error: %v", err)
	}
	if res.MessagesIngested != 0 {
		t.Fatalf("a thread cached at the same history id should be skipped, got %+v", res)
	}
}

func TestSyncTopicExplicitWindowOverridesWatermark(t *testing.T) {
	db := newTestStore(t)
	top, err := db.CreateTopic("newsletters", "Newsletters", "category:promotions")
	if err != nil {
		t.Fatalf("creating topic: %v", err)
	}
	// Mark the topic as freshly synced, which would normally pin the lookback
	// to the watermark.
	recent := time.Now().Add(-2 * time.Hour).UnixMilli()
	if err := db.UpdateTopicSyncState(top.ID, "100", recent); err != nil {
		t.Fatalf("seeding sync state: %v", err)
	}

	client := &fakeFetcher{}
	engine := newEngine(t, db, client)

	if _, err := engine.SyncTopic(t.Context(), "newsletters", Options{
		Since:         window(t, "1y"),
		SinceExplicit: true,
	}); err != nil {
		t.Fatalf("SyncTopic unexpected error: %v", err)
	}

	if len(client.queries) != 1 {
		t.Fatalf("expected one query, got %v", client.queries)
	}
	cutoff := time.Now().Add(-365 * 24 * time.Hour).UTC().Format(gmailDateLayout)
	if !strings.Contains(client.queries[0], "after:"+cutoff) {
		t.Fatalf("query %q should honour the explicitly requested one year window (after:%s)", client.queries[0], cutoff)
	}
}

func TestSyncTopicUsesWatermarkWhenWindowNotRequested(t *testing.T) {
	db := newTestStore(t)
	top, err := db.CreateTopic("newsletters", "Newsletters", "category:promotions")
	if err != nil {
		t.Fatalf("creating topic: %v", err)
	}
	watermark := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	if err := db.UpdateTopicSyncState(top.ID, "100", watermark.UnixMilli()); err != nil {
		t.Fatalf("seeding sync state: %v", err)
	}

	client := &fakeFetcher{}
	if _, err := newEngine(t, db, client).SyncTopic(t.Context(), "newsletters", Options{Since: window(t, "4w")}); err != nil {
		t.Fatalf("SyncTopic unexpected error: %v", err)
	}

	want := watermark.Add(-warmStartBuffer).Format(gmailDateLayout)
	if !strings.Contains(client.queries[0], "after:"+want) {
		t.Fatalf("query %q should resume from the watermark (after:%s)", client.queries[0], want)
	}
}

func TestSyncTopicUnboundedWindowSendsNoDateFilter(t *testing.T) {
	db := newTestStore(t)
	if _, err := db.CreateTopic("newsletters", "Newsletters", "category:promotions"); err != nil {
		t.Fatalf("creating topic: %v", err)
	}

	client := &fakeFetcher{}
	if _, err := newEngine(t, db, client).SyncTopic(t.Context(), "newsletters", Options{
		Since:         window(t, "all"),
		SinceExplicit: true,
	}); err != nil {
		t.Fatalf("SyncTopic unexpected error: %v", err)
	}

	if strings.Contains(client.queries[0], "after:") {
		t.Fatalf("an unbounded window should send no date filter, got %q", client.queries[0])
	}
}

func TestSyncQueryCachesMailWithoutRegisteringATopic(t *testing.T) {
	db := newTestStore(t)
	client := &fakeFetcher{
		threads: []gws.ThreadSummary{{ID: "t-1", HistoryID: "1"}},
		details: map[string]*gws.ThreadDetail{"t-1": threadWithMessage("t-1", "1", "1780000000000")},
	}

	res, err := newEngine(t, db, client).SyncQuery(t.Context(), "is:unread", Options{Since: window(t, "7d")})
	if err != nil {
		t.Fatalf("SyncQuery unexpected error: %v", err)
	}
	if res.Target != "is:unread" {
		t.Fatalf("Target = %q, want the query that was synced", res.Target)
	}
	if res.MessagesIngested != 1 {
		t.Fatalf("SyncQuery result = %+v", res)
	}

	// An ad-hoc query is not a saved search, so nothing may be registered.
	topics, err := db.ListTopics()
	if err != nil {
		t.Fatalf("listing topics: %v", err)
	}
	if len(topics) != 0 {
		t.Fatalf("a one-off query registered %d topic(s): %+v", len(topics), topics)
	}

	// The cached mail belongs to no topic.
	messages, err := db.SearchMessages("", "", false, 0)
	if err != nil {
		t.Fatalf("searching messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("cached %d messages, want 1", len(messages))
	}
	if messages[0].TopicID != nil {
		t.Fatalf("an ad-hoc message should have no topic, got %d", *messages[0].TopicID)
	}
}

func TestSyncQueryAppliesTheWindow(t *testing.T) {
	client := &fakeFetcher{}

	if _, err := newEngine(t, newTestStore(t), client).SyncQuery(t.Context(), "is:unread", Options{
		Since:         window(t, "7d"),
		SinceExplicit: true,
	}); err != nil {
		t.Fatalf("SyncQuery unexpected error: %v", err)
	}

	cutoff := time.Now().Add(-7 * 24 * time.Hour).UTC().Format(gmailDateLayout)
	if !strings.Contains(client.queries[0], "after:"+cutoff) {
		t.Fatalf("query %q should carry the requested lookback (after:%s)", client.queries[0], cutoff)
	}
}

func TestSyncQueryBudgetsFetches(t *testing.T) {
	client := &fakeFetcher{details: map[string]*gws.ThreadDetail{}}
	datedThreads(client, 5, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))

	res, err := newEngine(t, newTestStore(t), client).SyncQuery(t.Context(), "is:unread", Options{
		Since:      window(t, "all"),
		MaxThreads: 2,
	})
	if err != nil {
		t.Fatalf("SyncQuery unexpected error: %v", err)
	}
	if res.ThreadsFetched != 2 || res.ThreadsRemaining != 3 {
		t.Fatalf("SyncQuery result = %+v, want 2 fetched and 3 remaining", res)
	}
	if res.Complete() {
		t.Fatal("a budgeted ad-hoc sync must not report itself complete")
	}
}

func TestSyncQueryReportsListFailure(t *testing.T) {
	client := &fakeFetcher{listErr: errors.New("gws unavailable")}

	if _, err := newEngine(t, newTestStore(t), client).SyncQuery(t.Context(), "is:unread", Options{Since: window(t, "7d")}); err == nil {
		t.Fatal("SyncQuery should report a listing failure")
	}
}

// datedThreads builds count threads ordered newest first, one day apart, the
// way Gmail returns them.
func datedThreads(client *fakeFetcher, count int, newest time.Time) {
	for i := range count {
		id := fmt.Sprintf("t-%02d", i)
		when := newest.Add(-time.Duration(i) * 24 * time.Hour)
		client.threads = append(client.threads, gws.ThreadSummary{ID: id, HistoryID: fmt.Sprint(1000 - i)})
		client.details[id] = threadWithMessage(id, fmt.Sprint(1000-i), fmt.Sprint(when.UnixMilli()))
	}
}

func TestSyncTopicBudgetsFetchesAndReportsTheRemainder(t *testing.T) {
	db := newTestStore(t)
	client := &fakeFetcher{details: map[string]*gws.ThreadDetail{}}
	datedThreads(client, 5, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC))

	res, err := newEngine(t, db, client).SyncTopic(t.Context(), "news", Options{Since: window(t, "all"), MaxThreads: 2})
	if err != nil {
		t.Fatalf("SyncTopic unexpected error: %v", err)
	}

	if res.ThreadsListed != 5 {
		t.Fatalf("ThreadsListed = %d, want all 5 in the window", res.ThreadsListed)
	}
	if res.ThreadsFetched != 2 {
		t.Fatalf("ThreadsFetched = %d, want the budget of 2", res.ThreadsFetched)
	}
	if res.ThreadsRemaining != 3 {
		t.Fatalf("ThreadsRemaining = %d, want the 3 the budget did not reach", res.ThreadsRemaining)
	}
	if res.Complete() {
		t.Fatal("a sync that left conversations uncollected must not report itself complete")
	}
}

func TestSyncTopicDrainsWhenGetReportsADifferentHistoryID(t *testing.T) {
	// threads.get can report a different history ID than threads.list did.
	// Caching that value would leave the thread looking uncached on the next
	// run, so the budget would be spent re-fetching the same conversations and
	// the backlog would never drain.
	db := newTestStore(t)
	client := &fakeFetcher{details: map[string]*gws.ThreadDetail{}}
	for i := range 4 {
		id := fmt.Sprintf("t-%d", i)
		client.threads = append(client.threads, gws.ThreadSummary{ID: id, HistoryID: fmt.Sprint(i + 1)})
		// Every fetch reports the same, unrelated history ID.
		client.details[id] = threadWithMessage(id, "999", "1780000000000")
	}

	engine := newEngine(t, db, client)
	opts := Options{Since: window(t, "all"), SinceExplicit: true, MaxThreads: 2}

	for run, want := range []int{2, 0} {
		res, err := engine.SyncTopic(t.Context(), "news", opts)
		if err != nil {
			t.Fatalf("sync %d: %v", run+1, err)
		}
		if res.ThreadsRemaining != want {
			t.Fatalf("after sync %d, ThreadsRemaining = %d, want %d: the backlog is not draining", run+1, res.ThreadsRemaining, want)
		}
	}

	stats, err := db.Stats()
	if err != nil {
		t.Fatalf("reading stats: %v", err)
	}
	if stats.Threads != 4 {
		t.Fatalf("cached %d conversations, want all 4", stats.Threads)
	}
}

func TestSyncTopicHoldsWatermarkWhileBacklogRemains(t *testing.T) {
	db := newTestStore(t)
	client := &fakeFetcher{details: map[string]*gws.ThreadDetail{}}
	newest := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	datedThreads(client, 5, newest)

	if _, err := newEngine(t, db, client).SyncTopic(t.Context(), "news", Options{Since: window(t, "all"), MaxThreads: 2}); err != nil {
		t.Fatalf("SyncTopic unexpected error: %v", err)
	}

	top, err := db.GetTopicBySlug("news")
	if err != nil {
		t.Fatalf("reading topic: %v", err)
	}
	// Moving the mark to the newest collected message would strand the older
	// conversations the budget never reached.
	if top.LastMessageInternalDate != 0 {
		t.Fatalf("watermark advanced to %s while %d conversations were still uncollected",
			time.UnixMilli(top.LastMessageInternalDate).UTC(), 3)
	}
	if top.LastSyncedAt == nil {
		t.Fatal("the run should still be recorded as having happened")
	}
}

func TestSyncTopicDrainsBacklogAndThenAdvancesWatermark(t *testing.T) {
	db := newTestStore(t)
	client := &fakeFetcher{details: map[string]*gws.ThreadDetail{}}
	newest := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	datedThreads(client, 5, newest)

	engine := newEngine(t, db, client)
	opts := Options{Since: window(t, "all"), SinceExplicit: true, MaxThreads: 2}

	// Repeated runs must work through the backlog instead of re-reading the
	// same newest conversations forever.
	wantRemaining := []int{3, 1, 0}
	for run, want := range wantRemaining {
		res, err := engine.SyncTopic(t.Context(), "news", opts)
		if err != nil {
			t.Fatalf("sync %d: %v", run+1, err)
		}
		if res.ThreadsRemaining != want {
			t.Fatalf("after sync %d, ThreadsRemaining = %d, want %d", run+1, res.ThreadsRemaining, want)
		}
	}

	stats, err := db.Stats()
	if err != nil {
		t.Fatalf("reading stats: %v", err)
	}
	if stats.Messages != 5 {
		t.Fatalf("cached %d messages after draining, want all 5", stats.Messages)
	}

	// Only now that nothing is outstanding may the watermark move.
	top, err := db.GetTopicBySlug("news")
	if err != nil {
		t.Fatalf("reading topic: %v", err)
	}
	if got := time.UnixMilli(top.LastMessageInternalDate).UTC(); !got.Equal(newest) {
		t.Fatalf("watermark = %s, want the newest message %s once the window is complete", got, newest)
	}
}

func TestSyncTopicDeduplicatesAcrossQueries(t *testing.T) {
	db := newTestStore(t)
	top, err := db.CreateTopic("news", "News", "first")
	if err != nil {
		t.Fatalf("creating topic: %v", err)
	}
	if err := db.AddQuery(top.ID, "second"); err != nil {
		t.Fatalf("adding query: %v", err)
	}

	client := &fakeFetcher{
		threads: []gws.ThreadSummary{{ID: "t-1", HistoryID: "1"}},
		details: map[string]*gws.ThreadDetail{"t-1": threadWithMessage("t-1", "1", "1780000000000")},
	}

	res, err := newEngine(t, db, client).SyncTopic(t.Context(), "news", Options{Since: window(t, "4w")})
	if err != nil {
		t.Fatalf("SyncTopic unexpected error: %v", err)
	}
	if len(client.queries) != 2 {
		t.Fatalf("expected both queries to run, got %v", client.queries)
	}
	if res.ThreadsFetched != 1 {
		t.Fatalf("the same thread from two queries should be fetched once, got %d", res.ThreadsFetched)
	}
}

func TestSyncTopicSkipsUnreadableThread(t *testing.T) {
	db := newTestStore(t)
	client := &fakeFetcher{
		threads:    []gws.ThreadSummary{{ID: "t-1", HistoryID: "1"}, {ID: "t-2", HistoryID: "1"}},
		details:    map[string]*gws.ThreadDetail{"t-2": threadWithMessage("t-2", "1", "1780000000000")},
		detailFail: map[string]bool{"t-1": true},
	}

	res, err := newEngine(t, db, client).SyncTopic(t.Context(), "news", Options{Since: window(t, "4w")})
	if err != nil {
		t.Fatalf("one unreadable thread should not fail the sync: %v", err)
	}
	if res.MessagesIngested != 1 {
		t.Fatalf("expected the readable thread to be ingested, got %+v", res)
	}
}

func TestSyncTopicReportsListFailure(t *testing.T) {
	db := newTestStore(t)
	client := &fakeFetcher{listErr: errors.New("gws unavailable")}

	if _, err := newEngine(t, db, client).SyncTopic(t.Context(), "news", Options{Since: window(t, "4w")}); err == nil {
		t.Fatal("SyncTopic should report a listing failure")
	}
}

func TestSyncTopicSurfacesLookupFailure(t *testing.T) {
	db := newTestStore(t)
	db.Close()

	_, err := newEngine(t, db, &fakeFetcher{}).SyncTopic(t.Context(), "news", Options{Since: window(t, "4w")})
	if err == nil {
		t.Fatal("a database failure must not be mistaken for an unregistered topic")
	}
	if errors.Is(err, store.ErrTopicNotFound) {
		t.Fatalf("database failure was misreported as a missing topic: %v", err)
	}
}

func TestSyncAllTopicsCollectsPerTopicErrors(t *testing.T) {
	db := newTestStore(t)
	for _, slug := range []string{"alpha", "beta"} {
		if _, err := db.CreateTopic(slug, slug, slug); err != nil {
			t.Fatalf("creating topic %q: %v", slug, err)
		}
	}

	client := &fakeFetcher{listErr: errors.New("gws unavailable")}
	results, err := newEngine(t, db, client).SyncAllTopics(t.Context(), Options{Since: window(t, "4w")})
	if err != nil {
		t.Fatalf("SyncAllTopics unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected a result per topic, got %d", len(results))
	}
	for _, r := range results {
		if r.Error == "" {
			t.Fatalf("expected %q to report its failure, got %+v", r.Target, r)
		}
	}
}

func TestSyncAllTopicsSucceeds(t *testing.T) {
	db := newTestStore(t)
	if _, err := db.CreateTopic("alpha", "alpha", "alpha"); err != nil {
		t.Fatalf("creating topic: %v", err)
	}

	client := &fakeFetcher{
		threads: []gws.ThreadSummary{{ID: "t-1", HistoryID: "1"}},
		details: map[string]*gws.ThreadDetail{"t-1": threadWithMessage("t-1", "1", "1780000000000")},
	}

	results, err := newEngine(t, db, client).SyncAllTopics(t.Context(), Options{Since: window(t, "4w")})
	if err != nil {
		t.Fatalf("SyncAllTopics unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].MessagesIngested != 1 {
		t.Fatalf("SyncAllTopics results = %+v", results)
	}
}

func TestResolveCutoff(t *testing.T) {
	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	watermark := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	synced := watermark

	tests := []struct {
		name string
		top  *store.Topic
		opts Options
		want time.Time
	}{
		{
			name: "cold topic uses the default window",
			top:  &store.Topic{},
			opts: Options{Since: window(t, "4w")},
			want: now.Add(-4 * 7 * 24 * time.Hour),
		},
		{
			name: "warm topic resumes from the watermark",
			top:  &store.Topic{LastSyncedAt: &synced, LastMessageInternalDate: watermark.UnixMilli()},
			opts: Options{Since: window(t, "4w")},
			want: watermark.Add(-warmStartBuffer),
		},
		{
			name: "force full ignores the watermark",
			top:  &store.Topic{LastSyncedAt: &synced, LastMessageInternalDate: watermark.UnixMilli()},
			opts: Options{Since: window(t, "4w"), ForceFull: true},
			want: now.Add(-4 * 7 * 24 * time.Hour),
		},
		{
			name: "explicit window beats the watermark",
			top:  &store.Topic{LastSyncedAt: &synced, LastMessageInternalDate: watermark.UnixMilli()},
			opts: Options{Since: window(t, "1y"), SinceExplicit: true},
			want: now.Add(-365 * 24 * time.Hour),
		},
		{
			name: "explicit unbounded window clears the cutoff",
			top:  &store.Topic{LastSyncedAt: &synced, LastMessageInternalDate: watermark.UnixMilli()},
			opts: Options{Since: window(t, "all"), SinceExplicit: true},
			want: time.Time{},
		},
		{
			name: "cold topic with an unbounded default has no cutoff",
			top:  &store.Topic{},
			opts: Options{Since: window(t, "all")},
			want: time.Time{},
		},
	}

	engine := newEngine(t, newTestStore(t), &fakeFetcher{})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := engine.resolveCutoff(tt.top, tt.opts, now)
			if !got.Equal(tt.want) {
				t.Fatalf("resolveCutoff = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestMaxThreads(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want int
	}{
		{name: "unset falls back to the default", opts: Options{}, want: DefaultMaxThreads},
		{name: "negative falls back to the default", opts: Options{MaxThreads: -5}, want: DefaultMaxThreads},
		{name: "explicit value is kept", opts: Options{MaxThreads: 7}, want: 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maxThreads(tt.opts); got != tt.want {
				t.Fatalf("maxThreads = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSyncTopicStopsOnCancelledContext(t *testing.T) {
	db := newTestStore(t)
	client := &fakeFetcher{
		threads:   []gws.ThreadSummary{{ID: "t-1", HistoryID: "1"}},
		detailErr: context.Canceled,
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := newEngine(t, db, client).SyncTopic(ctx, "news", Options{Since: window(t, "4w")}); err == nil {
		t.Fatal("a cancelled context should abort the sync rather than skipping threads")
	}
}

func TestSyncAllTopicsStopsOnCancelledContext(t *testing.T) {
	db := newTestStore(t)
	for _, slug := range []string{"alpha", "beta"} {
		if _, err := db.CreateTopic(slug, slug, slug); err != nil {
			t.Fatalf("creating topic %q: %v", slug, err)
		}
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	results, err := newEngine(t, db, &fakeFetcher{}).SyncAllTopics(ctx, Options{Since: window(t, "4w")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SyncAllTopics error = %v, want context.Canceled", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected the loop to stop after the first topic, got %d results", len(results))
	}
}

func TestSyncTopicReportsIngestFailure(t *testing.T) {
	db := newTestStore(t)
	// A message whose internalDate cannot be parsed must fail the sync rather
	// than silently store an epoch-zero timestamp.
	detail := threadWithMessage("t-1", "1", "not-a-number")
	client := &fakeFetcher{
		threads: []gws.ThreadSummary{{ID: "t-1", HistoryID: "1"}},
		details: map[string]*gws.ThreadDetail{"t-1": detail},
	}

	if _, err := newEngine(t, db, client).SyncTopic(t.Context(), "news", Options{Since: window(t, "4w")}); err == nil {
		t.Fatal("SyncTopic should report an unparseable message timestamp")
	}
}

func TestResolveQueriesReportsStoreFailure(t *testing.T) {
	db := newTestStore(t)
	db.Close()

	if _, err := newEngine(t, db, &fakeFetcher{}).resolveQueries(&store.Topic{ID: 1, Slug: "s"}); err == nil {
		t.Fatal("resolveQueries should report a database failure instead of silently falling back")
	}
}

func TestResolveQueriesFallbacks(t *testing.T) {
	db := newTestStore(t)
	engine := newEngine(t, db, &fakeFetcher{})

	tests := []struct {
		name string
		top  *store.Topic
		want string
	}{
		{name: "falls back to the topic query", top: &store.Topic{ID: -1, Slug: "s", Query: "from:billing@"}, want: "from:billing@"},
		{name: "falls back to the display name", top: &store.Topic{ID: -1, Slug: "s", DisplayName: "Billing"}, want: "Billing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := engine.resolveQueries(tt.top)
			if err != nil {
				t.Fatalf("resolveQueries unexpected error: %v", err)
			}
			if len(got) != 1 || got[0] != tt.want {
				t.Fatalf("resolveQueries = %v, want [%q]", got, tt.want)
			}
		})
	}
}

// Package mailsync keeps the local cache up to date with Gmail using a
// high-watermark incremental sync per tracked topic.
package mailsync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alexgorbatchev/gws-cache-cli/internal/classifier"
	"github.com/alexgorbatchev/gws-cache-cli/internal/gws"
	"github.com/alexgorbatchev/gws-cache-cli/internal/message"
	"github.com/alexgorbatchev/gws-cache-cli/internal/store"
	"github.com/alexgorbatchev/gws-cache-cli/internal/timewindow"
)

const (
	// DefaultMaxThreads caps how many threads a single sync will fetch when
	// the caller does not set a limit.
	DefaultMaxThreads = 25

	// warmStartBuffer overlaps each incremental sync with the previous one, so
	// messages that arrived while the last sync was running are not missed.
	warmStartBuffer = 24 * time.Hour

	// gmailDateLayout is the date format accepted by Gmail's "after:" operator.
	gmailDateLayout = "2006-01-02"

	statusSuccess = "success"
	statusFailed  = "failed"
)

// ThreadFetcher is the part of the gws client this engine depends on.
type ThreadFetcher interface {
	ListThreads(ctx context.Context, query string) ([]gws.ThreadSummary, error)
	GetThread(ctx context.Context, threadID string) (*gws.ThreadDetail, error)
}

// Options controls a single sync.
type Options struct {
	// ForceFull re-fetches every thread in the window, ignoring the cached
	// history IDs and the incremental watermark.
	ForceFull bool

	// Since bounds how far back to look.
	Since timewindow.Window

	// SinceExplicit reports whether the user chose the window themselves. An
	// explicit window always wins, so that asking for a wider lookback reaches
	// further back than the incremental watermark would allow. Otherwise a
	// previously synced topic resumes from its watermark and Since applies
	// only to the first sync.
	SinceExplicit bool

	// MaxThreads caps the threads fetched per sync; zero means DefaultMaxThreads.
	MaxThreads int
}

// Result reports what one sync accomplished.
type Result struct {
	// Target names what was synced: a topic slug, or the ad-hoc query when the
	// run belonged to no saved search.
	Target string `json:"target"`

	// ThreadsListed is how many unique conversations the window contained.
	ThreadsListed int `json:"threads_listed"`

	// ThreadsFetched is how many were newly downloaded during this run.
	ThreadsFetched int `json:"threads_fetched"`

	// ThreadsRemaining is how many uncached conversations this run's budget
	// did not reach. While it is above zero the topic is incompletely cached,
	// and the high-watermark is deliberately held back so a later run can
	// still collect them.
	ThreadsRemaining int `json:"threads_remaining"`

	MessagesIngested int    `json:"messages_ingested"`
	MaxHistoryID     string `json:"max_history_id"`
	Error            string `json:"error,omitempty"`
}

// Complete reports whether the window was fully cached by this run.
func (r Result) Complete() bool { return r.ThreadsRemaining == 0 }

// Engine synchronizes tracked topics into the cache.
type Engine struct {
	store    *store.DB
	client   ThreadFetcher
	ingestor *message.Ingestor

	// Progress receives human-readable status lines. It is never nil.
	Progress func(format string, args ...any)
}

// NewEngine returns an Engine writing to db, fetching through client and
// classifying with rules.
func NewEngine(db *store.DB, client ThreadFetcher, rules *classifier.Classifier) *Engine {
	return &Engine{
		store:    db,
		client:   client,
		ingestor: message.NewIngestor(db, rules),
		Progress: func(string, ...any) {},
	}
}

// SyncTopic synchronizes one topic, registering it first if it is not tracked
// yet, and records the outcome as a sync run.
func (e *Engine) SyncTopic(ctx context.Context, slug string, opts Options) (*Result, error) {
	top, err := e.resolveTopic(slug)
	if err != nil {
		return nil, err
	}

	res, syncErr := e.run(ctx, &top.ID, top.Slug, func() (*Result, error) {
		return e.syncTopic(ctx, top, opts)
	})
	if syncErr != nil {
		return res, syncErr
	}
	return res, nil
}

// SyncQuery synchronizes an ad-hoc Gmail query. Nothing is registered and the
// cached mail belongs to no saved search, which is what separates it from a
// topic sync.
func (e *Engine) SyncQuery(ctx context.Context, query string, opts Options) (*Result, error) {
	return e.run(ctx, nil, query, func() (*Result, error) {
		return e.syncQuery(ctx, query, opts)
	})
}

// run brackets a sync with its run record, so that both entry points report
// success and failure the same way.
func (e *Engine) run(ctx context.Context, topicID *int64, target string, sync func() (*Result, error)) (*Result, error) {
	runID, err := e.store.StartSyncRun(topicID)
	if err != nil {
		return nil, fmt.Errorf("starting sync run for %q: %w", target, err)
	}

	res, syncErr := sync()
	if res == nil {
		res = &Result{Target: target}
	}

	status, message := statusSuccess, ""
	if syncErr != nil {
		status, message = statusFailed, syncErr.Error()
	}
	if err := e.store.CompleteSyncRun(runID, status, res.ThreadsFetched, res.MessagesIngested, res.MaxHistoryID, message); err != nil {
		e.Progress("Warning: could not record sync run %d: %v", runID, err)
	}

	return res, syncErr
}

// syncQuery caches the threads matching an ad-hoc query. There is no watermark
// to keep, because nothing is tracking the query between runs.
func (e *Engine) syncQuery(ctx context.Context, query string, opts Options) (*Result, error) {
	now := time.Now()
	res := &Result{Target: query}

	threads, err := e.collectThreads(ctx, query, []string{query}, e.resolveCutoff(nil, opts, now))
	if err != nil {
		return res, err
	}

	res.ThreadsListed = len(threads)
	e.Progress("Found %d conversation(s) for %q", len(threads), query)

	if err := e.ingestThreads(ctx, nil, threads, res, now, maxThreads(opts)); err != nil {
		return res, err
	}
	return res, nil
}

// resolveTopic loads a topic, creating it on demand. Only a genuine
// "not found" registers a new topic: any other failure is a database problem
// and must surface rather than silently produce a new row.
func (e *Engine) resolveTopic(slug string) (*store.Topic, error) {
	top, err := e.store.GetTopicBySlug(slug)
	switch {
	case err == nil:
		return top, nil
	case errors.Is(err, store.ErrTopicNotFound):
		created, createErr := e.store.CreateTopic(slug, slug, "")
		if createErr != nil {
			return nil, fmt.Errorf("auto-registering topic %q: %w", slug, createErr)
		}
		return created, nil
	default:
		return nil, fmt.Errorf("looking up topic %q: %w", slug, err)
	}
}

func (e *Engine) syncTopic(ctx context.Context, top *store.Topic, opts Options) (*Result, error) {
	now := time.Now()
	res := &Result{Target: top.Slug}

	queries, err := e.resolveQueries(top)
	if err != nil {
		return res, err
	}

	threads, err := e.collectThreads(ctx, top.Slug, queries, e.resolveCutoff(top, opts, now))
	if err != nil {
		return res, err
	}

	res.ThreadsListed = len(threads)
	e.Progress("Found %d conversation(s) for topic %q", len(threads), top.Slug)

	if err := e.ingestThreads(ctx, &top.ID, threads, res, now, maxThreads(opts)); err != nil {
		return res, err
	}

	// Advancing the watermark past conversations this run never collected
	// would strand them: every later incremental sync starts after them, and
	// not even a full re-sync looks that far back again. So the mark only
	// moves once the window is completely cached. Passing zero still records
	// that a sync ran, without moving the mark.
	var watermark int64
	if res.Complete() {
		watermark, err = e.store.MaxMessageInternalDate(top.ID)
		if err != nil {
			return res, err
		}
	} else {
		e.Progress("Holding the sync watermark for %q: %d conversation(s) still to collect", top.Slug, res.ThreadsRemaining)
	}

	if err := e.store.UpdateTopicSyncState(top.ID, res.MaxHistoryID, watermark); err != nil {
		return res, fmt.Errorf("recording sync state for topic %q: %w", top.Slug, err)
	}
	return res, nil
}

// resolveQueries returns the Gmail queries that define a topic, falling back to
// the topic's own query and finally its display name.
func (e *Engine) resolveQueries(top *store.Topic) ([]string, error) {
	queries, err := e.store.ListQueries(top.ID)
	if err != nil {
		return nil, fmt.Errorf("listing queries for topic %q: %w", top.Slug, err)
	}
	if len(queries) > 0 {
		return queries, nil
	}
	if top.Query != "" {
		return []string{top.Query}, nil
	}
	return []string{top.DisplayName}, nil
}

// resolveCutoff decides how far back this sync should reach. The second result
// is false when no lower bound applies at all.
func (e *Engine) resolveCutoff(top *store.Topic, opts Options, now time.Time) time.Time {
	if opts.SinceExplicit {
		if start, ok := opts.Since.Start(now); ok {
			return start
		}
		return time.Time{}
	}

	warmStart := top != nil && !opts.ForceFull && top.LastSyncedAt != nil && top.LastMessageInternalDate > 0
	if warmStart {
		return time.UnixMilli(top.LastMessageInternalDate).Add(-warmStartBuffer).UTC()
	}

	if start, ok := opts.Since.Start(now); ok {
		return start
	}
	return time.Time{}
}

func maxThreads(opts Options) int {
	if opts.MaxThreads <= 0 {
		return DefaultMaxThreads
	}
	return opts.MaxThreads
}

// collectThreads runs every query for a topic and returns the unique threads
// the window contains. Nothing is dropped here: the per-run budget is spent in
// ingestThreads, on the expensive fetches, so that a run which cannot finish
// still leaves an accurate picture of what is outstanding.
func (e *Engine) collectThreads(ctx context.Context, target string, queries []string, cutoff time.Time) ([]gws.ThreadSummary, error) {
	var collected []gws.ThreadSummary
	seen := make(map[string]bool)

	for _, query := range queries {
		scoped := query
		if !cutoff.IsZero() {
			scoped = fmt.Sprintf("%s after:%s", query, cutoff.UTC().Format(gmailDateLayout))
		}

		e.Progress("Querying Gmail for %q: %q...", target, scoped)
		threads, err := e.client.ListThreads(ctx, scoped)
		if err != nil {
			return nil, fmt.Errorf("listing threads for %q: %w", target, err)
		}

		for _, thread := range threads {
			if seen[thread.ID] {
				continue
			}
			seen[thread.ID] = true
			collected = append(collected, thread)
		}
	}

	return collected, nil
}

// ingestThreads caches each thread that is not already stored at the same
// history ID, spending at most budget fetches. Anything the budget does not
// reach is counted in res.ThreadsRemaining rather than dropped, so the next
// run can pick it up.
func (e *Engine) ingestThreads(ctx context.Context, topicID *int64, threads []gws.ThreadSummary, res *Result, now time.Time, budget int) error {
	spent := 0

	for i, summary := range threads {
		cached, err := e.store.HasThreadWithHistory(summary.ID, summary.HistoryID)
		if err != nil {
			return fmt.Errorf("checking cached thread %s: %w", summary.ID, err)
		}
		if cached {
			e.Progress("[%d/%d] Thread %s cached at history %s (skipped)", i+1, len(threads), summary.ID, summary.HistoryID)
			continue
		}

		// Cached conversations cost nothing, so the budget is spent only on
		// ones that actually need downloading. Counting the rest instead of
		// stopping keeps the reported backlog exact.
		if spent >= budget {
			res.ThreadsRemaining++
			continue
		}
		spent++

		detail, err := e.client.GetThread(ctx, summary.ID)
		if err != nil {
			// One unreadable thread should not abandon the whole sync, but a
			// cancelled context means the user asked us to stop.
			if ctx.Err() != nil {
				return fmt.Errorf("fetching thread %s: %w", summary.ID, err)
			}
			e.Progress("Warning: skipping thread %s: %v", summary.ID, err)
			continue
		}

		// Cache the history ID the *listing* reported, because that is the
		// value the next listing is compared against. Storing the one that
		// threads.get returns instead would leave a thread looking uncached
		// whenever the two endpoints disagree, so every run would re-fetch it,
		// spend the whole budget on it, and never drain the backlog.
		historyID := summary.HistoryID
		if historyID == "" {
			historyID = detail.HistoryID
		}
		if detail.HistoryID > res.MaxHistoryID {
			res.MaxHistoryID = detail.HistoryID
		}

		stats, err := e.ingestor.IngestThread(detail, topicID, historyID, summary.Snippet, now)
		if err != nil {
			return err
		}
		res.MessagesIngested += stats.MessagesIngested
		res.ThreadsFetched++

		e.Progress("[%d/%d] Ingested thread %s (%d msgs)", i+1, len(threads), summary.ID, stats.MessagesIngested)
	}

	return nil
}

// SyncAllTopics synchronizes every tracked topic, reporting per-topic failures
// in the results rather than abandoning the remaining topics.
func (e *Engine) SyncAllTopics(ctx context.Context, opts Options) ([]Result, error) {
	topics, err := e.store.ListTopics()
	if err != nil {
		return nil, fmt.Errorf("listing topics: %w", err)
	}

	results := make([]Result, 0, len(topics))
	for _, top := range topics {
		res, err := e.SyncTopic(ctx, top.Slug, opts)
		switch {
		case err != nil:
			results = append(results, Result{Target: top.Slug, Error: err.Error()})
		case res != nil:
			results = append(results, *res)
		}
		if ctx.Err() != nil {
			return results, ctx.Err()
		}
	}

	return results, nil
}

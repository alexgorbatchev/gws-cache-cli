// Package calendar caches Google Calendar events, using sync tokens for
// incremental delta ingestion and a bounded window for full syncs.
package event

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/alexgorbatchev/gws-cache-cli/internal/gws"
	"github.com/alexgorbatchev/gws-cache-cli/internal/store"
	"github.com/alexgorbatchev/gws-cache-cli/internal/timewindow"
)

const (
	// DefaultCalendarID is the calendar synced when none is named.
	DefaultCalendarID = "primary"

	// statusCancelled marks events Google reports as removed from the calendar.
	statusCancelled = "cancelled"

	// defaultEventDuration is assumed when an event carries no end time.
	defaultEventDuration = 30 * time.Minute

	// dateLayout is Google's all-day event date format.
	dateLayout = "2006-01-02"

	// progressInterval throttles per-event progress lines on large syncs.
	progressInterval = 5
)

// Lister is the part of the gws client this engine depends on.
type Lister interface {
	ListCalendarEvents(ctx context.Context, params gws.CalendarListParams) (*gws.CalendarEventsResponse, error)
}

// SyncOptions controls a calendar sync.
type SyncOptions struct {
	CalendarID string

	// Past and Future bound a full sync. An unbounded window asks Google for
	// everything in that direction rather than falling back to a default.
	Past   timewindow.Window
	Future timewindow.Window

	// ForceFull ignores any stored sync token and re-syncs the whole window.
	ForceFull bool
}

// SyncResult reports what a calendar sync did.
type SyncResult struct {
	CalendarID     string `json:"calendar_id"`
	EventsFetched  int    `json:"events_fetched"`
	EventsIngested int    `json:"events_ingested"`
	EventsDeleted  int    `json:"events_deleted"`

	// SyncToken is Google's delta cursor. It is internal bookkeeping and is
	// deliberately kept out of the serialized output.
	SyncToken string `json:"-"`
}

// Engine synchronizes a Google Calendar into the cache.
type Engine struct {
	store  *store.DB
	client Lister

	// Progress receives human-readable status lines. It is never nil.
	Progress func(format string, args ...any)
}

// NewEngine returns an Engine writing to db and fetching through client.
func NewEngine(db *store.DB, client Lister) *Engine {
	return &Engine{
		store:    db,
		client:   client,
		Progress: func(string, ...any) {},
	}
}

// Sync ingests calendar events, preferring an incremental delta sync
// when a usable sync token is stored.
func (e *Engine) Sync(ctx context.Context, opts SyncOptions) (*SyncResult, error) {
	now := time.Now()

	calendarID := opts.CalendarID
	if calendarID == "" {
		calendarID = DefaultCalendarID
	}

	windowStart, hasStart := opts.Past.Start(now)
	windowEnd, hasEnd := opts.Future.End(now)

	storedState, err := e.store.GetCalendarSyncState(calendarID)
	if err != nil {
		return nil, fmt.Errorf("reading sync state for calendar %s: %w", calendarID, err)
	}

	resp, err := e.fetchEvents(ctx, calendarID, opts.ForceFull, storedState, windowStart, hasStart, windowEnd, hasEnd)
	if err != nil {
		return nil, err
	}

	e.Progress("Fetched %d calendar event(s)", len(resp.Items))

	matcher, err := e.newTopicMatcher()
	if err != nil {
		return nil, err
	}

	res := &SyncResult{
		CalendarID:    calendarID,
		EventsFetched: len(resp.Items),
		SyncToken:     resp.NextSyncToken,
	}

	for i, item := range resp.Items {
		if err := e.ingestEvent(item, calendarID, matcher, res); err != nil {
			return nil, err
		}
		if (i+1)%progressInterval == 0 || i+1 == len(resp.Items) {
			e.Progress("[%d/%d] Ingested calendar event: %q", i+1, len(resp.Items), item.Summary)
		}
	}

	if err := e.recordSyncState(calendarID, resp.NextSyncToken, storedState, windowStart, windowEnd); err != nil {
		return res, err
	}

	e.Progress("Calendar sync complete: %d ingested (%d cancelled/deleted).", res.EventsIngested, res.EventsDeleted)
	return res, nil
}

// fetchEvents performs a delta sync when a token is available, falling back to
// a windowed sync when Google rejects the token as expired.
func (e *Engine) fetchEvents(
	ctx context.Context,
	calendarID string,
	forceFull bool,
	state *store.CalendarSyncState,
	windowStart time.Time, hasStart bool,
	windowEnd time.Time, hasEnd bool,
) (*gws.CalendarEventsResponse, error) {
	windowed := gws.CalendarListParams{CalendarID: calendarID}
	if hasStart {
		windowed.TimeMin = windowStart.Format(time.RFC3339)
	}
	if hasEnd {
		windowed.TimeMax = windowEnd.Format(time.RFC3339)
	}

	if !forceFull && state != nil && state.SyncToken != "" {
		e.Progress("Running incremental delta sync for calendar %q...", calendarID)
		resp, err := e.client.ListCalendarEvents(ctx, gws.CalendarListParams{
			CalendarID: calendarID,
			SyncToken:  state.SyncToken,
		})
		if err == nil {
			return resp, nil
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("listing calendar events: %w", err)
		}
		e.Progress("Sync token rejected, falling back to a windowed re-sync...")
	}

	e.Progress("Running windowed sync for calendar %q (%s)...", calendarID, describeWindow(windowed))
	resp, err := e.client.ListCalendarEvents(ctx, windowed)
	if err != nil {
		return nil, fmt.Errorf("listing calendar events: %w", err)
	}
	return resp, nil
}

// describeWindow renders the requested bounds for progress output.
func describeWindow(params gws.CalendarListParams) string {
	from, to := "the beginning", "the end of time"
	if params.TimeMin != "" {
		from = params.TimeMin[:len(dateLayout)]
	}
	if params.TimeMax != "" {
		to = params.TimeMax[:len(dateLayout)]
	}
	return from + " to " + to
}

func (e *Engine) ingestEvent(item gws.CalendarEventDetail, calendarID string, matcher topicMatcher, res *SyncResult) error {
	cancelled := item.Status == statusCancelled
	if cancelled {
		res.EventsDeleted++
	}

	attendees, err := json.Marshal(item.Attendees)
	if err != nil {
		return fmt.Errorf("encoding attendees for event %s: %w", item.ID, err)
	}

	start, end := parseEventTimes(item, time.Now())

	record := &store.CalendarEvent{
		ID:             item.ID,
		CalendarID:     calendarID,
		TopicID:        matcher.match(item.Summary, item.Description),
		Summary:        item.Summary,
		Description:    item.Description,
		Location:       item.Location,
		OrganizerEmail: item.Organizer.Email,
		StartTime:      start,
		EndTime:        end,
		Status:         item.Status,
		IsDeleted:      cancelled,
		AttendeesJSON:  string(attendees),
		HTMLLink:       item.HTMLLink,
		ICalUID:        item.ICalUID,
	}

	if err := e.store.UpsertCalendarEvent(record); err != nil {
		return fmt.Errorf("upserting calendar event %s: %w", item.ID, err)
	}
	res.EventsIngested++
	return nil
}

// recordSyncState stores the token for the next delta sync, keeping the
// previous token when Google returned none.
func (e *Engine) recordSyncState(calendarID, nextToken string, state *store.CalendarSyncState, windowStart, windowEnd time.Time) error {
	if nextToken == "" && state != nil {
		nextToken = state.SyncToken
	}

	if err := e.store.UpdateCalendarSyncState(&store.CalendarSyncState{
		CalendarID:  calendarID,
		SyncToken:   nextToken,
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
	}); err != nil {
		return fmt.Errorf("recording sync state for calendar %s: %w", calendarID, err)
	}
	return nil
}

// topicRule links a compiled whole-word pattern to the topic it identifies.
type topicRule struct {
	pattern *regexp.Regexp
	topicID int64
}

// topicMatcher assigns calendar events to tracked topics. Rules are held in a
// deterministic order, longest term first, so the same event always resolves to
// the same topic no matter how the topics were loaded.
type topicMatcher struct {
	rules []topicRule
}

func (e *Engine) newTopicMatcher() (topicMatcher, error) {
	topics, err := e.store.ListTopics()
	if err != nil {
		return topicMatcher{}, fmt.Errorf("listing topics for event matching: %w", err)
	}

	type term struct {
		text    string
		topicID int64
	}
	var terms []term
	for _, top := range topics {
		terms = append(terms, term{text: top.Slug, topicID: top.ID})
		if top.DisplayName != "" && !strings.EqualFold(top.DisplayName, top.Slug) {
			terms = append(terms, term{text: top.DisplayName, topicID: top.ID})
		}
	}

	// Longest term first so a specific name wins over a shorter one that
	// happens to be a prefix; ties break on the text, then the topic ID, so
	// the order never depends on map iteration or row ordering.
	sort.Slice(terms, func(i, j int) bool {
		if len(terms[i].text) != len(terms[j].text) {
			return len(terms[i].text) > len(terms[j].text)
		}
		if terms[i].text != terms[j].text {
			return terms[i].text < terms[j].text
		}
		return terms[i].topicID < terms[j].topicID
	})

	matcher := topicMatcher{}
	for _, t := range terms {
		// Whole-word matching keeps a short slug such as "ai" from matching
		// inside an unrelated word such as "airline".
		pattern, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(t.text) + `\b`)
		if err != nil {
			return topicMatcher{}, fmt.Errorf("compiling matcher for topic term %q: %w", t.text, err)
		}
		matcher.rules = append(matcher.rules, topicRule{pattern: pattern, topicID: t.topicID})
	}
	return matcher, nil
}

// match returns the topic an event belongs to, or nil when none applies.
func (m topicMatcher) match(summary, description string) *int64 {
	for _, rule := range m.rules {
		if rule.pattern.MatchString(summary) || rule.pattern.MatchString(description) {
			topicID := rule.topicID
			return &topicID
		}
	}
	return nil
}

// parseEventTimes resolves an event's start and end, covering both timed and
// all-day events and supplying sensible values when either is missing.
func parseEventTimes(item gws.CalendarEventDetail, now time.Time) (time.Time, time.Time) {
	start := parseEventTime(item.Start.DateTime, item.Start.Date)
	end := parseEventTime(item.End.DateTime, item.End.Date)

	if start.IsZero() {
		start = now.UTC()
	}
	if end.IsZero() {
		end = start.Add(defaultEventDuration)
	}
	return start.UTC(), end.UTC()
}

func parseEventTime(dateTime, date string) time.Time {
	if dateTime != "" {
		if parsed, err := time.Parse(time.RFC3339, dateTime); err == nil {
			return parsed
		}
	}
	if date != "" {
		if parsed, err := time.Parse(dateLayout, date); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

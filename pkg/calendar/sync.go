package calendar

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gws-cache/pkg/gmail"
	"gws-cache/pkg/store"
	"gws-cache/pkg/sync"
)

type CalendarEngine struct {
	store    *store.DB
	client   gmail.Client
	Progress func(format string, args ...any)
}

func NewCalendarEngine(s *store.DB, c gmail.Client) *CalendarEngine {
	return &CalendarEngine{
		store:  s,
		client: c,
		Progress: func(format string, args ...any) {
			// default no-op
		},
	}
}

type SyncOptions struct {
	CalendarID string // default "primary"
	Past       string // default "4w"
	Future     string // default "4w"
	ForceFull  bool
}

type SyncResult struct {
	CalendarID     string `json:"calendar_id"`
	EventsFetched  int    `json:"events_fetched"`
	EventsIngested int    `json:"events_ingested"`
	EventsDeleted  int    `json:"events_deleted"`
	SyncToken      string `json:"sync_token"`
}

func (e *CalendarEngine) SyncCalendar(opts SyncOptions) (*SyncResult, error) {
	calID := opts.CalendarID
	if calID == "" {
		calID = "primary"
	}

	now := time.Now()

	// Parse past lookback
	pastSec, err := sync.ParseSince(opts.Past)
	if err != nil {
		return nil, fmt.Errorf("invalid past lookback: %w", err)
	}
	windowStart := now.Add(-4 * 7 * 24 * time.Hour).UTC()
	if pastSec > 0 {
		windowStart = time.Unix(pastSec, 0).UTC()
	}

	// Parse future lookahead
	futureSec, err := sync.ParseSince(opts.Future)
	if err != nil {
		return nil, fmt.Errorf("invalid future lookahead: %w", err)
	}
	windowEnd := now.Add(4 * 7 * 24 * time.Hour).UTC()
	if futureSec > 0 {
		diffSec := futureSec - now.Unix()
		if diffSec < 0 {
			diffSec = -diffSec
		}
		windowEnd = now.Add(time.Duration(diffSec) * time.Second).UTC()
	}

	storedState, _ := e.store.GetCalendarSyncState(calID)

	params := gmail.CalendarListParams{
		CalendarID: calID,
	}

	useSyncToken := !opts.ForceFull && storedState != nil && storedState.SyncToken != ""
	if useSyncToken {
		params.SyncToken = storedState.SyncToken
		e.Progress("Running incremental delta sync for calendar %q...", calID)
	} else {
		params.TimeMin = windowStart.Format(time.RFC3339)
		params.TimeMax = windowEnd.Format(time.RFC3339)
		e.Progress("Running windowed sync for calendar %q (%s to %s)...", calID, windowStart.Format("2006-01-02"), windowEnd.Format("2006-01-02"))
	}

	resp, err := e.client.ListCalendarEvents(params)
	if err != nil {
		if useSyncToken {
			e.Progress("Sync token expired or invalid. Falling back to windowed re-sync...")
			params.SyncToken = ""
			params.TimeMin = windowStart.Format(time.RFC3339)
			params.TimeMax = windowEnd.Format(time.RFC3339)
			resp, err = e.client.ListCalendarEvents(params)
		}
		if err != nil {
			return nil, fmt.Errorf("listing calendar events: %w", err)
		}
	}

	e.Progress("Fetched %d calendar event(s) from Google Calendar", len(resp.Items))

	res := &SyncResult{
		CalendarID:    calID,
		EventsFetched: len(resp.Items),
		SyncToken:     resp.NextSyncToken,
	}

	topics, _ := e.store.ListTopics()
	slugToTopicID := make(map[string]int64)
	for _, t := range topics {
		slugToTopicID[strings.ToLower(t.Slug)] = t.ID
		if t.DisplayName != "" {
			slugToTopicID[strings.ToLower(t.DisplayName)] = t.ID
		}
	}

	for i, item := range resp.Items {
		isCancelled := item.Status == "cancelled"
		if isCancelled {
			res.EventsDeleted++
		}

		startTime, endTime := parseEventTimes(item)
		attendeesJSON, _ := json.Marshal(item.Attendees)

		var matchedTopicID *int64
		summaryLower := strings.ToLower(item.Summary)
		descLower := strings.ToLower(item.Description)

		for key, id := range slugToTopicID {
			if strings.Contains(summaryLower, key) || strings.Contains(descLower, key) {
				topID := id
				matchedTopicID = &topID
				break
			}
		}

		eventRecord := &store.CalendarEvent{
			ID:             item.ID,
			CalendarID:     calID,
			TopicID:        matchedTopicID,
			Summary:        item.Summary,
			Description:    item.Description,
			Location:       item.Location,
			OrganizerEmail: item.Organizer.Email,
			StartTime:      startTime,
			EndTime:        endTime,
			Status:         item.Status,
			IsDeleted:      isCancelled,
			AttendeesJSON:  string(attendeesJSON),
			HTMLLink:       item.HTMLLink,
			ICalUID:        item.ICalUID,
		}

		if err := e.store.UpsertCalendarEvent(eventRecord); err != nil {
			return nil, fmt.Errorf("upserting calendar event %s: %w", item.ID, err)
		}
		res.EventsIngested++

		if (i+1)%5 == 0 || i+1 == len(resp.Items) {
			e.Progress("[%d/%d] Ingested calendar event: %q", i+1, len(resp.Items), item.Summary)
		}
	}

	nextSyncToken := resp.NextSyncToken
	if nextSyncToken == "" && storedState != nil {
		nextSyncToken = storedState.SyncToken
	}

	_ = e.store.UpdateCalendarSyncState(&store.CalendarSyncState{
		CalendarID:  calID,
		SyncToken:   nextSyncToken,
		WindowStart: windowStart,
		WindowEnd:   windowEnd,
	})

	e.Progress("Calendar sync complete: %d ingested (%d cancelled/deleted).", res.EventsIngested, res.EventsDeleted)
	return res, nil
}

func parseEventTimes(item gmail.CalendarEventDetail) (time.Time, time.Time) {
	var start, end time.Time

	if item.Start.DateTime != "" {
		start, _ = time.Parse(time.RFC3339, item.Start.DateTime)
	} else if item.Start.Date != "" {
		start, _ = time.Parse("2006-01-02", item.Start.Date)
	}

	if item.End.DateTime != "" {
		end, _ = time.Parse(time.RFC3339, item.End.DateTime)
	} else if item.End.Date != "" {
		end, _ = time.Parse("2006-01-02", item.End.Date)
	}

	if start.IsZero() {
		start = time.Now().UTC()
	}
	if end.IsZero() {
		end = start.Add(30 * time.Minute)
	}

	return start.UTC(), end.UTC()
}

func extractDomain(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return strings.ToLower(parts[1])
	}
	return ""
}

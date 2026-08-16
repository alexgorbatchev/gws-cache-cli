package calendar

import (
	"errors"
	"path/filepath"
	"testing"

	"gws-cache/pkg/gmail"
	"gws-cache/pkg/store"
)

type mockCalendarClient struct {
	resp      *gmail.CalendarEventsResponse
	listErr   error
	callCount int
}

func (m *mockCalendarClient) ListThreads(query string) ([]gmail.ThreadSummary, error) {
	return nil, nil
}

func (m *mockCalendarClient) GetThread(threadID string) (*gmail.ThreadDetail, error) {
	return nil, nil
}

func (m *mockCalendarClient) ListCalendarEvents(p gmail.CalendarListParams) (*gmail.CalendarEventsResponse, error) {
	m.callCount++
	if m.listErr != nil {
		return nil, m.listErr
	}
	if p.SyncToken == "invalid-token" && m.callCount == 1 {
		return nil, errors.New("sync token expired")
	}
	if m.resp != nil {
		return m.resp, nil
	}

	evt1 := gmail.CalendarEventDetail{
		ID:          "evt-1",
		Status:      "confirmed",
		Summary:     "Call with Newsletters",
		Description: "Editorial sync",
	}
	evt1.Start.DateTime = "2026-07-29T11:00:00Z"
	evt1.End.DateTime = "2026-07-29T12:00:00Z"
	evt1.Organizer.Email = "editor@newsletter.com"
	evt1.Attendees = []gmail.CalendarAttendee{
		{Email: "editor@newsletter.com", DisplayName: "Editor"},
	}

	evt2 := gmail.CalendarEventDetail{
		ID:     "evt-2",
		Status: "cancelled",
	}
	evt2.Start.Date = "2026-07-27"
	evt2.End.Date = "2026-07-27"

	return &gmail.CalendarEventsResponse{
		NextSyncToken: "sync-100",
		Items:         []gmail.CalendarEventDetail{evt1, evt2},
	}, nil
}

func TestCalendarEngine(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	top, err := db.CreateTopic("newsletters", "Newsletters", "category:promotions")
	if err != nil {
		t.Fatalf("CreateTopic failed: %v", err)
	}
	_ = top

	mockClient := &mockCalendarClient{}
	engine := NewCalendarEngine(db, mockClient)

	// Cold Start Sync
	res, err := engine.SyncCalendar(SyncOptions{Past: "4w", Future: "4w"})
	if err != nil {
		t.Fatalf("SyncCalendar failed: %v", err)
	}
	if res.EventsFetched != 2 || res.EventsDeleted != 1 {
		t.Fatalf("unexpected sync result: %+v", res)
	}

	// Warm Start Sync (uses stored syncToken)
	resWarm, err := engine.SyncCalendar(SyncOptions{})
	if err != nil {
		t.Fatalf("SyncCalendar warm start failed: %v", err)
	}
	if resWarm.CalendarID != "primary" {
		t.Fatalf("unexpected warm sync result: %+v", resWarm)
	}

	// Test SyncToken fallback on error
	_ = db.UpdateCalendarSyncState(&store.CalendarSyncState{
		CalendarID: "primary",
		SyncToken:  "invalid-token",
	})
	mockClient.callCount = 0
	resFallback, err := engine.SyncCalendar(SyncOptions{})
	if err != nil {
		t.Fatalf("expected sync token fallback to succeed, got: %v", err)
	}
	if resFallback.EventsFetched != 2 {
		t.Fatalf("unexpected fallback sync result: %+v", resFallback)
	}

	// Verify events in DB
	events, err := db.ListCalendarEvents("primary", "newsletters", 0)
	if err != nil {
		t.Fatalf("ListCalendarEvents failed: %v", err)
	}
	if len(events) != 1 || events[0].Summary != "Call with Newsletters" {
		t.Fatalf("unexpected calendar events list: %+v", events)
	}

	// Error test
	mockClient.listErr = errors.New("calendar api error")
	_, err = engine.SyncCalendar(SyncOptions{ForceFull: true})
	if err == nil {
		t.Fatal("expected error on calendar API failure")
	}
}

func TestParseEventTimes(t *testing.T) {
	// All day event
	itemDate := gmail.CalendarEventDetail{}
	itemDate.Start.Date = "2026-08-15"
	itemDate.End.Date = "2026-08-16"
	start, end := parseEventTimes(itemDate)
	if start.IsZero() || end.IsZero() {
		t.Fatal("expected non-zero dates for all-day event")
	}

	// Empty event fallback
	itemEmpty := gmail.CalendarEventDetail{}
	startEmpty, endEmpty := parseEventTimes(itemEmpty)
	if startEmpty.IsZero() || endEmpty.IsZero() {
		t.Fatal("expected non-zero fallback dates")
	}
}

func TestExtractDomain(t *testing.T) {
	if extractDomain("editor@newsletter.com") != "newsletter.com" {
		t.Fatalf("unexpected extractDomain result")
	}
	if extractDomain("invalidemail") != "" {
		t.Fatalf("expected empty domain for invalid email")
	}
}

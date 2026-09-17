package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultDBPath(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. XDG_CACHE_HOME explicitly set
	t.Setenv("XDG_CACHE_HOME", tmpDir)
	pathXDG := DefaultDBPath()
	expectedXDG := filepath.Join(tmpDir, "gws-cache", "cache.db")
	if pathXDG != expectedXDG {
		t.Fatalf("expected XDG path %q, got %q", expectedXDG, pathXDG)
	}

	// 2. Default UserCacheDir / HOME
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("HOME", tmpDir)
	defaultPath := DefaultDBPath()
	if defaultPath == "" {
		t.Fatal("expected non-empty default DB path")
	}

	// 3. Fallback when HOME is invalid/empty
	t.Setenv("HOME", "")
	fallbackPath := DefaultDBPath()
	if fallbackPath == "" {
		t.Fatal("expected fallback path")
	}
}

func TestStoreOperations(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	// 1. Create Topic
	top, err := db.CreateTopic("newsletters", "Tech Newsletters", "category:promotions")
	if err != nil {
		t.Fatalf("CreateTopic failed: %v", err)
	}
	if top.Slug != "newsletters" || top.DisplayName != "Tech Newsletters" {
		t.Fatalf("unexpected topic data: %+v", top)
	}

	// Create topic with empty display name
	top2, err := db.CreateTopic("billing", "", "from:billing@")
	if err != nil {
		t.Fatalf("CreateTopic with empty name failed: %v", err)
	}
	if top2.DisplayName != "billing" {
		t.Fatalf("expected display name 'billing', got %q", top2.DisplayName)
	}

	// Create duplicate topic slug error
	if _, err := db.CreateTopic("newsletters", "Tech Newsletters", "category:promotions"); err == nil {
		t.Fatal("expected error creating duplicate topic slug")
	}

	// List Topics
	topicsList, err := db.ListTopics()
	if err != nil || len(topicsList) != 2 {
		t.Fatalf("ListTopics failed: %v, count: %d", err, len(topicsList))
	}

	// GetTopicBySlug error
	if _, err := db.GetTopicBySlug("nonexistent"); err == nil {
		t.Fatal("expected error for nonexistent topic")
	}

	// 2. Add and List Queries
	if err := db.AddQuery(top.ID, "label:tech"); err != nil {
		t.Fatalf("AddQuery failed: %v", err)
	}
	queries, err := db.ListQueries(top.ID)
	if err != nil {
		t.Fatalf("ListQueries failed: %v", err)
	}
	if len(queries) < 2 {
		t.Fatalf("expected at least 2 queries, got %d", len(queries))
	}

	// 3. Upsert Thread and HasThreadWithHistory
	if err := db.UpsertThread("t-101", &top.ID, "hist-101", "Newsletter issue #1"); err != nil {
		t.Fatalf("UpsertThread failed: %v", err)
	}
	hasThread, err := db.HasThreadWithHistory("t-101", "hist-101")
	if err != nil || !hasThread {
		t.Fatalf("HasThreadWithHistory failed or false: %v", err)
	}

	// UpsertThread with topic_id 0 updates existing without changing topic_id
	if err := db.UpsertThread("t-101", nil, "hist-102", "Updated snippet"); err != nil {
		t.Fatalf("UpsertThread update failed: %v", err)
	}

	// 4. Upsert Message
	msg := &Message{
		ID:                   "m-101",
		ThreadID:             "t-101",
		TopicID:              &top.ID,
		InternalDate:         1770000000000,
		DateISO:              "2026-02-01T12:00:00Z",
		FromAddress:          "editor@newsletter.com",
		FromName:             "Editor",
		ToAddress:            "me@example.com",
		Subject:              "Weekly Tech Digest",
		Snippet:              "Here is the weekly summary...",
		BodyPlain:            "Here is the weekly summary of tech news.",
		IsAutomated:          false,
		ClassificationReason: "Human newsletter editor",
	}
	if err := db.UpsertMessage(msg); err != nil {
		t.Fatalf("UpsertMessage failed: %v", err)
	}

	// UpsertMessage update with topic_id 0
	msg.Snippet = "Updated snippet content"
	msg.TopicID = nil
	if err := db.UpsertMessage(msg); err != nil {
		t.Fatalf("UpsertMessage update failed: %v", err)
	}

	// 5. List Messages by Topic
	msgs, err := db.ListMessagesByTopic(top.ID, false, 0)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ListMessagesByTopic failed: %v, msgs count: %d", err, len(msgs))
	}

	// Human-only filter & sinceSec filter
	humanMsgs, err := db.ListMessagesByTopic(top.ID, true, 1000)
	if err != nil || len(humanMsgs) != 1 {
		t.Fatalf("ListMessagesByTopic humanOnly failed: %v", err)
	}

	// 6. Search Messages
	searched, err := db.SearchMessages("Weekly", "newsletters", true, 1000)
	if err != nil || len(searched) != 1 {
		t.Fatalf("SearchMessages failed: %v, found: %d", err, len(searched))
	}

	// SearchMessages without parameters
	allSearched, err := db.SearchMessages("", "", false, 0)
	if err != nil || len(allSearched) != 1 {
		t.Fatalf("SearchMessages all failed: %v, count: %d", err, len(allSearched))
	}

	// 7. Update Topic Sync State
	if err := db.UpdateTopicSyncState(top.ID, "hist-101", 1770000001000); err != nil {
		t.Fatalf("UpdateTopicSyncState failed: %v", err)
	}

	// 8. Start and Complete Sync Run
	runID, err := db.StartSyncRun(&top.ID)
	if err != nil {
		t.Fatalf("StartSyncRun failed: %v", err)
	}
	if err := db.CompleteSyncRun(runID, "success", 1, 1, "hist-101", ""); err != nil {
		t.Fatalf("CompleteSyncRun failed: %v", err)
	}

	// 9. Calendar Operations
	calState, err := db.GetCalendarSyncState("nonexistent")
	if err != nil || calState != nil {
		t.Fatalf("expected nil state for nonexistent calendar, got %+v", calState)
	}

	calState = &CalendarSyncState{
		CalendarID:  "primary",
		SyncToken:   "token-123",
		WindowStart: time.Now().Add(-24 * time.Hour).UTC(),
		WindowEnd:   time.Now().Add(24 * time.Hour).UTC(),
	}
	if err := db.UpdateCalendarSyncState(calState); err != nil {
		t.Fatalf("UpdateCalendarSyncState failed: %v", err)
	}
	gotState, err := db.GetCalendarSyncState("primary")
	if err != nil || gotState == nil || gotState.SyncToken != "token-123" {
		t.Fatalf("GetCalendarSyncState failed: %v, state: %+v", err, gotState)
	}

	event := &CalendarEvent{
		ID:             "evt-101",
		CalendarID:     "primary",
		TopicID:        &top.ID,
		Summary:        "Newsletter Editorial Call",
		Description:    "Discuss next week's issue",
		Location:       "Google Meet",
		OrganizerEmail: "editor@newsletter.com",
		StartTime:      time.Now().UTC(),
		EndTime:        time.Now().Add(time.Hour).UTC(),
		Status:         "confirmed",
		IsDeleted:      false,
	}
	if err := db.UpsertCalendarEvent(event); err != nil {
		t.Fatalf("UpsertCalendarEvent failed: %v", err)
	}

	// UpsertCalendarEvent with nil TopicID
	eventNoTopic := &CalendarEvent{
		ID:         "evt-102",
		CalendarID: "primary",
		TopicID:    nil,
		Summary:    "Generic Meeting",
		StartTime:  time.Now().UTC(),
		EndTime:    time.Now().Add(time.Hour).UTC(),
		Status:     "confirmed",
	}
	if err := db.UpsertCalendarEvent(eventNoTopic); err != nil {
		t.Fatalf("UpsertCalendarEvent no topic failed: %v", err)
	}

	events, err := db.SearchCalendarEvents("", "primary", "newsletters", 1000)
	if err != nil || len(events) != 1 {
		t.Fatalf("ListCalendarEvents failed: %v, count: %d", err, len(events))
	}

	eventsAll, err := db.SearchCalendarEvents("", "", "", 0)
	if err != nil || len(eventsAll) != 2 {
		t.Fatalf("ListCalendarEvents all failed: %v, count: %d", err, len(eventsAll))
	}

	// 10. Stats
	stats, err := db.Stats()
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if want := (Stats{Topics: 2, Threads: 1, Messages: 1}); stats != want {
		t.Fatalf("Stats = %+v, want %+v", stats, want)
	}

	// 11. Delete Topic
	if err := db.DeleteTopic("newsletters"); err != nil {
		t.Fatalf("DeleteTopic failed: %v", err)
	}
	if err := db.DeleteTopic("nonexistent"); err == nil {
		t.Fatal("expected error deleting nonexistent topic")
	}

	if _, err := db.GetTopicBySlug("newsletters"); err == nil {
		t.Fatal("expected error getting deleted topic")
	}
}

func TestStoreErrorsOnClosedDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "closed.db")

	db, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	db.Close() // Close immediately to trigger error branches

	if _, err := db.HasThreadWithHistory("t", "h"); err == nil {
		t.Fatal("expected error on closed db")
	}
	if err := db.UpsertCalendarEvent(&CalendarEvent{ID: "e"}); err == nil {
		t.Fatal("expected error on closed db")
	}
	if _, err := db.GetCalendarSyncState("c"); err == nil {
		t.Fatal("expected error on closed db")
	}
	if err := db.UpdateCalendarSyncState(&CalendarSyncState{CalendarID: "c"}); err == nil {
		t.Fatal("expected error on closed db")
	}
	if _, err := db.SearchCalendarEvents("", "c", "s", 0); err == nil {
		t.Fatal("expected error on closed db")
	}
	if _, err := db.CreateTopic("s", "n", "q"); err == nil {
		t.Fatal("expected error on closed db")
	}
	if _, err := db.ListTopics(); err == nil {
		t.Fatal("expected error on closed db")
	}
	if err := db.DeleteTopic("s"); err == nil {
		t.Fatal("expected error on closed db")
	}
	if err := db.AddQuery(1, "q"); err == nil {
		t.Fatal("expected error on closed db")
	}
	if _, err := db.ListQueries(1); err == nil {
		t.Fatal("expected error on closed db")
	}
	if err := db.UpsertThread("t", nil, "h", "s"); err == nil {
		t.Fatal("expected error on closed db")
	}
	if err := db.UpsertMessage(&Message{ID: "m"}); err == nil {
		t.Fatal("expected error on closed db")
	}
	if err := db.UpdateTopicSyncState(1, "h", 100); err == nil {
		t.Fatal("expected error on closed db")
	}
	if _, err := db.ListMessagesByTopic(1, false, 0); err == nil {
		t.Fatal("expected error on closed db")
	}
	if _, err := db.SearchMessages("k", "s", false, 0); err == nil {
		t.Fatal("expected error on closed db")
	}
	if _, err := db.StartSyncRun(nil); err == nil {
		t.Fatal("expected error on closed db")
	}
	if err := db.CompleteSyncRun(1, "f", 0, 0, "", ""); err == nil {
		t.Fatal("expected error on closed db")
	}
	if _, err := db.Stats(); err == nil {
		t.Fatal("expected error on closed db")
	}
	if _, err := db.MaxMessageInternalDate(1); err == nil {
		t.Fatal("expected error on closed db")
	}
}

func TestTopicNotFoundIsASentinel(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "sentinel.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	_, err = db.GetTopicBySlug("absent")
	if !errors.Is(err, ErrTopicNotFound) {
		t.Fatalf("GetTopicBySlug error = %v, want it to wrap ErrTopicNotFound", err)
	}

	if err := db.DeleteTopic("absent"); !errors.Is(err, ErrTopicNotFound) {
		t.Fatalf("DeleteTopic error = %v, want it to wrap ErrTopicNotFound", err)
	}
}

func TestMaxMessageInternalDate(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "watermark.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	top, err := db.CreateTopic("news", "News", "q")
	if err != nil {
		t.Fatalf("CreateTopic failed: %v", err)
	}

	got, err := db.MaxMessageInternalDate(top.ID)
	if err != nil {
		t.Fatalf("MaxMessageInternalDate failed: %v", err)
	}
	if got != 0 {
		t.Fatalf("an empty topic should report zero, got %d", got)
	}

	if err := db.UpsertThread("t-1", &top.ID, "h", "s"); err != nil {
		t.Fatalf("UpsertThread failed: %v", err)
	}
	for id, millis := range map[string]int64{"m-1": 1_700_000_000_000, "m-2": 1_800_000_000_000} {
		if err := db.UpsertMessage(&Message{
			ID: id, ThreadID: "t-1", TopicID: &top.ID,
			InternalDate: millis,
			DateISO:      time.UnixMilli(millis).UTC().Format(time.RFC3339),
			FromAddress:  "a@example.com", ToAddress: "b@example.com",
			Subject: id, Snippet: id,
		}); err != nil {
			t.Fatalf("UpsertMessage failed: %v", err)
		}
	}

	got, err = db.MaxMessageInternalDate(top.ID)
	if err != nil {
		t.Fatalf("MaxMessageInternalDate failed: %v", err)
	}
	if got != 1_800_000_000_000 {
		t.Fatalf("MaxMessageInternalDate = %d, want the newest cached message", got)
	}
}

func TestListMessagesByTopicFiltersByWindow(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "window.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	top, err := db.CreateTopic("news", "News", "q")
	if err != nil {
		t.Fatalf("CreateTopic failed: %v", err)
	}
	if err := db.UpsertThread("t-1", &top.ID, "h-1", "s"); err != nil {
		t.Fatalf("UpsertThread failed: %v", err)
	}

	const oldSeconds, newSeconds = 1_700_000_000, 1_800_000_000
	for id, seconds := range map[string]int64{"m-old": oldSeconds, "m-new": newSeconds} {
		if err := db.UpsertMessage(&Message{
			ID: id, ThreadID: "t-1", TopicID: &top.ID,
			InternalDate: seconds * 1000,
			DateISO:      time.Unix(seconds, 0).UTC().Format(time.RFC3339),
			FromAddress:  "a@example.com", ToAddress: "b@example.com",
			Subject: id, Snippet: id,
		}); err != nil {
			t.Fatalf("UpsertMessage failed: %v", err)
		}
	}

	all, err := db.ListMessagesByTopic(top.ID, false, 0)
	if err != nil || len(all) != 2 {
		t.Fatalf("unfiltered list = %d messages, err %v", len(all), err)
	}

	recent, err := db.ListMessagesByTopic(top.ID, false, newSeconds-1)
	if err != nil {
		t.Fatalf("filtered list failed: %v", err)
	}
	if len(recent) != 1 || recent[0].ID != "m-new" {
		t.Fatalf("filtered list = %+v, want only m-new", recent)
	}
}

func TestOpenError(t *testing.T) {
	_, err := Open("/dev/null/invalid/path/db.db")
	if err == nil {
		t.Fatal("expected error opening invalid path")
	}
}

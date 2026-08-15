package sync

import (
	"errors"
	"path/filepath"
	"testing"

	"gws-cache/pkg/gmail"
	"gws-cache/pkg/store"
)

type mockGmailClient struct {
	threads      []gmail.ThreadSummary
	details      map[string]*gmail.ThreadDetail
	listErr      error
	getThreadErr error
}

func (m *mockGmailClient) ListThreads(query string) ([]gmail.ThreadSummary, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.threads, nil
}

func (m *mockGmailClient) GetThread(threadID string) (*gmail.ThreadDetail, error) {
	if m.getThreadErr != nil {
		return nil, m.getThreadErr
	}
	if detail, ok := m.details[threadID]; ok {
		return detail, nil
	}
	return &gmail.ThreadDetail{ID: threadID}, nil
}

func (m *mockGmailClient) ListCalendarEvents(p gmail.CalendarListParams) (*gmail.CalendarEventsResponse, error) {
	return &gmail.CalendarEventsResponse{}, nil
}

func TestSyncEngine(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	top, err := db.CreateTopic("newsletters", "Tech Newsletters", "category:promotions")
	if err != nil {
		t.Fatalf("CreateTopic failed: %v", err)
	}
	_ = top

	mockClient := &mockGmailClient{
		threads: []gmail.ThreadSummary{
			{ID: "t1", HistoryID: "100", Snippet: "Weekly Tech Digest"},
		},
		details: map[string]*gmail.ThreadDetail{
			"t1": {
				ID:        "t1",
				HistoryID: "100",
				Messages: []gmail.MessageDetail{
					{
						ID:           "m1",
						ThreadID:     "t1",
						HistoryID:    "100",
						InternalDate: "1770000000000",
						Snippet:      "Here is your newsletter.",
						Payload: gmail.Payload{
							Headers: []gmail.Header{
								{Name: "Date", Value: "Mon, 20 Jul 2026 13:00:00 -0700"},
								{Name: "From", Value: "Editor <editor@newsletter.com>"},
								{Name: "To", Value: "alex@gmail.com"},
								{Name: "Subject", Value: "Weekly Tech Digest #10"},
							},
						},
					},
				},
			},
		},
	}

	engine := NewEngine(db, mockClient)
	opts := SyncOptions{Since: "4w", MaxThreads: 10}

	// Cold Start Sync
	res, err := engine.SyncTopic("newsletters", opts)
	if err != nil {
		t.Fatalf("SyncTopic failed: %v", err)
	}
	if res.ThreadsFetched != 1 || res.MessagesIngested != 1 {
		t.Fatalf("unexpected cold sync result: %+v", res)
	}

	// Warm Start Sync
	resWarm, err := engine.SyncTopic("newsletters", opts)
	if err != nil {
		t.Fatalf("SyncTopic warm start failed: %v", err)
	}
	if resWarm.TopicSlug != "newsletters" {
		t.Fatalf("unexpected warm sync result: %+v", resWarm)
	}

	// Sync All
	allRes, err := engine.SyncAllTopics(opts)
	if err != nil {
		t.Fatalf("SyncAllTopics failed: %v", err)
	}
	if len(allRes) != 1 {
		t.Fatalf("expected 1 result from SyncAllTopics, got %d", len(allRes))
	}

	// Sync Topic auto-registers new slug on the fly
	resAuto, err := engine.SyncTopic("billing", opts)
	if err != nil {
		t.Fatalf("expected auto-registration for billing, got error: %v", err)
	}
	if resAuto.TopicSlug != "billing" {
		t.Fatalf("unexpected auto sync result: %+v", resAuto)
	}

	// List threads error test
	mockClient.listErr = errors.New("list threads error")
	_, err = engine.SyncTopic("newsletters", opts)
	if err == nil {
		t.Fatal("expected error on ListThreads error")
	}
	mockClient.listErr = nil

	// Get thread warning/skip test on uncached thread
	mockClient.getThreadErr = errors.New("get thread warning")
	mockClient.threads = []gmail.ThreadSummary{
		{ID: "t999", HistoryID: "999", Snippet: "Uncached thread"},
	}
	resWarn, err := engine.SyncTopic("newsletters", opts)
	if err != nil {
		t.Fatalf("SyncTopic should handle thread error gracefully, got: %v", err)
	}
	if resWarn.MessagesIngested != 0 {
		t.Fatalf("expected 0 ingested messages on skipped thread, got %d", resWarn.MessagesIngested)
	}
	mockClient.getThreadErr = nil

	// Verify DB state
	topAfter, _ := db.GetTopicBySlug("newsletters")
	if topAfter.LastSyncedAt == nil {
		t.Fatal("expected LastSyncedAt to be non-nil after sync")
	}
}

func TestParseSince(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"4w", false},
		{"30d", false},
		{"2m", false},
		{"1y", false},
		{"24h", false},
		{"2026-06-01", false},
		{"all", false},
		{"", false},
		{"invalid", true},
		{"x", true},
		{"10x", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := ParseSince(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseSince(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestHelperParsing(t *testing.T) {
	iso := parseISO("Mon, 20 Jul 2026 13:00:00 -0700", 1770000000000)
	if iso == "" {
		t.Fatal("expected non-empty ISO string")
	}

	isoFallback := parseISO("Mon, 20 Jul 2026 13:00:00 -0700", 0)
	if isoFallback == "" {
		t.Fatal("expected non-empty ISO string from date header")
	}

	isoInvalid := parseISO("invalid date", 0)
	if isoInvalid == "" {
		t.Fatal("expected non-empty ISO string fallback")
	}

	addr, name := parseAddress(`"Leither Moise" <leither@envoy.com>`)
	if addr != "leither@envoy.com" || name != "Leither Moise" {
		t.Fatalf("parseAddress failed: got addr=%q name=%q", addr, name)
	}

	addrPlain, namePlain := parseAddress("leither@envoy.com")
	if addrPlain != "leither@envoy.com" || namePlain != "" {
		t.Fatalf("parseAddress plain failed: got addr=%q name=%q", addrPlain, namePlain)
	}

	emptyAddr, emptyName := parseAddress("")
	if emptyAddr != "" || emptyName != "" {
		t.Fatal("expected empty strings for empty address")
	}

	quoted := trimQuotes(`"test"`)
	if quoted != "test" {
		t.Fatalf("trimQuotes failed: %s", quoted)
	}
}

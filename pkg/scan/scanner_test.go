package scan

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

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

func TestScanner(t *testing.T) {
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

	nowMs := time.Now().UnixMilli()
	mockClient := &mockGmailClient{
		threads: []gmail.ThreadSummary{
			{ID: "scan-t10000", HistoryID: "500", Snippet: "Weekly Newsletter"},
			{ID: "scan-t2", HistoryID: "501", Snippet: "Second Newsletter"},
		},
		details: map[string]*gmail.ThreadDetail{
			"scan-t10000": {
				ID:        "scan-t10000",
				HistoryID: "500",
				Messages: []gmail.MessageDetail{
					{
						ID:           "scan-m1",
						ThreadID:     "scan-t10000",
						HistoryID:    "500",
						InternalDate: fmt.Sprintf("%d", nowMs),
						Snippet:      "Tech newsletter issue",
						Payload: gmail.Payload{
							Headers: []gmail.Header{
								{Name: "From", Value: "'Editor' <editor@tech.com>"},
								{Name: "Subject", Value: "Tech Weekly #1"},
								{Name: "Date", Value: "Mon, 20 Jul 2026 13:00:00 -0700"},
							},
						},
					},
				},
			},
			"scan-t2": {
				ID:        "scan-t2",
				HistoryID: "501",
				Messages: []gmail.MessageDetail{
					{
						ID:           "scan-m2",
						ThreadID:     "scan-t2",
						HistoryID:    "501",
						InternalDate: "0",
						Snippet:      "Automated notification",
						Payload: gmail.Payload{
							Headers: []gmail.Header{
								{Name: "From", Value: "no-reply@tech.com"},
								{Name: "Subject", Value: "Automated alert"},
								{Name: "Auto-Submitted", Value: "auto-generated"},
							},
						},
					},
				},
			},
		},
	}

	scanner := NewScanner(db, mockClient)

	// Scan Inbox invalid since
	_, err = scanner.ScanInbox(ScanOptions{Since: "invalid"})
	if err == nil {
		t.Fatal("expected error on invalid since")
	}

	// Scan Inbox ListThreads error
	mockClient.listErr = errors.New("list threads error")
	_, err = scanner.ScanInbox(ScanOptions{})
	if err == nil {
		t.Fatal("expected error on ListThreads error")
	}
	mockClient.listErr = nil

	// Scan Inbox with cap
	res, err := scanner.ScanInbox(ScanOptions{
		Query:      "",
		Since:      "7d",
		MaxThreads: 1,
	})
	if err != nil {
		t.Fatalf("ScanInbox failed: %v", err)
	}
	if res.ThreadsEvaluated != 1 || res.MessagesIngested != 1 {
		t.Fatalf("unexpected scan result: %+v", res)
	}

	// Scan Inbox second run (skips cached thread)
	res2, err := scanner.ScanInbox(ScanOptions{
		Query:      "is:unread",
		Since:      "",
		MaxThreads: 10,
	})
	if err != nil {
		t.Fatalf("ScanInbox run 2 failed: %v", err)
	}
	if res2.ThreadsSkipped != 1 {
		t.Fatalf("expected 1 thread skipped, got %d", res2.ThreadsSkipped)
	}

	// GetThread warning branch
	mockClient.getThreadErr = errors.New("get thread error")
	mockClient.threads = []gmail.ThreadSummary{{ID: "t-uncached", HistoryID: "999"}}
	resWarn, err := scanner.ScanInbox(ScanOptions{})
	if err != nil {
		t.Fatalf("ScanInbox warning branch failed: %v", err)
	}
	if resWarn.MessagesIngested != 0 {
		t.Fatalf("expected 0 ingested on error")
	}
	mockClient.getThreadErr = nil

	// Search Cached
	msgs, err := scanner.SearchCached(SearchOptions{
		Keyword:   "Tech",
		HumanOnly: true,
		Since:     "14d",
	})
	if err != nil {
		t.Fatalf("SearchCached failed: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 cached message, got %d", len(msgs))
	}

	// Search Cached invalid since
	_, err = scanner.SearchCached(SearchOptions{Since: "invalid"})
	if err == nil {
		t.Fatal("expected error on SearchCached invalid since")
	}
}

func TestParseISOAndAddress(t *testing.T) {
	iso1 := parseISO("", 1770000000000)
	if iso1 == "" {
		t.Fatal("expected ISO date from internalDate")
	}

	iso2 := parseISO("Mon, 20 Jul 2026 13:00:00 -0700", 0)
	if iso2 == "" {
		t.Fatal("expected ISO date from RFC1123Z")
	}

	iso3 := parseISO("invalid date", 0)
	if iso3 == "" {
		t.Fatal("expected fallback ISO date")
	}

	addr, name := parseAddress(`"Editor" <editor@tech.com>`)
	if addr != "editor@tech.com" || name != "Editor" {
		t.Fatalf("parseAddress failed: addr=%q name=%q", addr, name)
	}

	emptyAddr, emptyName := parseAddress("")
	if emptyAddr != "" || emptyName != "" {
		t.Fatal("expected empty strings for empty address")
	}

	q1 := trimQuotes(`'single'`)
	if q1 != "single" {
		t.Fatalf("trimQuotes single quote failed: %s", q1)
	}

	q2 := trimQuotes(`"double"`)
	if q2 != "double" {
		t.Fatalf("trimQuotes double quote failed: %s", q2)
	}
}

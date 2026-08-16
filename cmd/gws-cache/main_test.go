package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"gws-cache/pkg/gmail"
)

type mockCLIClient struct {
	gmail.CLIClient
}

func (m *mockCLIClient) ListCalendarEvents(p gmail.CalendarListParams) (*gmail.CalendarEventsResponse, error) {
	evt := gmail.CalendarEventDetail{
		ID:      "evt-1",
		Status:  "confirmed",
		Summary: "Call with Newsletters",
	}
	evt.Start.DateTime = "2026-07-29T11:00:00Z"
	evt.End.DateTime = "2026-07-29T12:00:00Z"

	return &gmail.CalendarEventsResponse{
		NextSyncToken: "sync-100",
		Items:         []gmail.CalendarEventDetail{evt},
	}, nil
}

func (m *mockCLIClient) ListThreads(query string) ([]gmail.ThreadSummary, error) {
	return []gmail.ThreadSummary{
		{ID: "t-1", HistoryID: "100", Snippet: "Weekly Tech Digest"},
	}, nil
}

func (m *mockCLIClient) GetThread(threadID string) (*gmail.ThreadDetail, error) {
	return &gmail.ThreadDetail{
		ID:        "t-1",
		HistoryID: "100",
		Messages: []gmail.MessageDetail{
			{
				ID:           "m-1",
				ThreadID:     "t-1",
				HistoryID:    "100",
				InternalDate: "1770000000000",
				Snippet:      "Here is your newsletter.",
				Payload: gmail.Payload{
					Headers: []gmail.Header{
						{Name: "From", Value: "editor@tech.com"},
						{Name: "Subject", Value: "Tech Weekly #1"},
						{Name: "Date", Value: "Mon, 20 Jul 2026 13:00:00 -0700"},
					},
				},
			},
		},
	}, nil
}

func TestCLICommands(t *testing.T) {
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, "test.db")

	newClient = func() gmail.Client {
		return &mockCLIClient{}
	}

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)

	// 1. Topic List Empty
	rootCmd.SetArgs([]string{"--db", testDB, "topic", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("topic list empty failed: %v", err)
	}
	if !strings.Contains(buf.String(), "No topics tracked yet.") {
		t.Fatalf("expected empty topic list message, got: %s", buf.String())
	}

	// 2. Calendar List Empty
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "calendar", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("calendar list empty failed: %v", err)
	}
	if !strings.Contains(buf.String(), "No calendar events found.") {
		t.Fatalf("expected empty calendar events message, got: %s", buf.String())
	}

	// 3. Search Empty
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "search", "nonexistent"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("search empty failed: %v", err)
	}
	if !strings.Contains(buf.String(), "No matching cached messages found.") {
		t.Fatalf("expected empty search message, got: %s", buf.String())
	}

	// 4. Topic Add
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "topic", "add", "newsletters", "--name", "Tech Newsletters", "--query", "category:promotions"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("topic add failed: %v", err)
	}

	// Topic Add error (duplicate)
	rootCmd.SetArgs([]string{"--db", testDB, "topic", "add", "newsletters"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error adding duplicate topic")
	}

	// 5. Topic List Non-empty
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "topic", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("topic list failed: %v", err)
	}
	if !strings.Contains(buf.String(), "newsletters") || !strings.Contains(buf.String(), "Tech Newsletters") {
		t.Fatalf("topic list missing added topic: %s", buf.String())
	}

	// 6. Sync Single Topic
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "sync", "newsletters"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("sync newsletters failed: %v", err)
	}

	// Sync Auto-register new topic
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "sync", "billing"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("sync auto-register billing failed: %v", err)
	}

	// 7. Calendar Sync Command
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "calendar", "sync", "--past", "4w", "--future", "4w"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("calendar sync failed: %v", err)
	}

	// 8. Calendar List Command
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "calendar", "list", "newsletters", "--format", "json", "--since", "all"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("calendar list json failed: %v", err)
	}

	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "calendar", "list", "--format", "table", "--since", "all"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("calendar list table failed: %v", err)
	}

	// 9. Scan Inbox Command
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "scan", "--since", "all"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	// 10. Search Command with results
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "search", "Tech", "--topic", "newsletters", "--human-only", "--since", "all", "--format", "table"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("search table failed: %v", err)
	}
	if !strings.Contains(buf.String(), "Tech Weekly") {
		t.Fatalf("expected search output to contain message subject, got: %s", buf.String())
	}

	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "search", "--since", "all", "--format", "json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("search json failed: %v", err)
	}
	if !strings.Contains(buf.String(), "m-1") {
		t.Fatalf("expected search json to contain message ID, got: %s", buf.String())
	}

	// 11. Sync All Topics
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "sync", "--all"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("sync --all failed: %v", err)
	}

	// 12. Status
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "status"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !strings.Contains(buf.String(), "Tracked Topics:  2") {
		t.Fatalf("unexpected status output: %s", buf.String())
	}

	// 13. Export
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "export", "newsletters", "--format", "json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("export failed: %v", err)
	}

	// Export error (nonexistent topic)
	rootCmd.SetArgs([]string{"--db", testDB, "export", "nonexistent"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error exporting nonexistent topic")
	}

	// 14. Topic Remove
	rootCmd.SetArgs([]string{"--db", testDB, "topic", "remove", "newsletters"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("topic remove failed: %v", err)
	}

	// Topic Remove error (nonexistent topic)
	rootCmd.SetArgs([]string{"--db", testDB, "topic", "remove", "nonexistent"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error removing nonexistent topic")
	}
}

func TestGetDB(t *testing.T) {
	dbPath = ""
	db, err := getDB()
	if err != nil {
		t.Fatalf("getDB failed: %v", err)
	}
	db.Close()

	dbPath = "/dev/null/invalid/db.db"
	_, err = getDB()
	if err == nil {
		t.Fatal("expected error on invalid db path")
	}
}

func TestTruncate(t *testing.T) {
	if truncate("hello", 10) != "hello" {
		t.Fatalf("unexpected truncate result")
	}
	if truncate("hello world", 6) != "hel..." {
		t.Fatalf("unexpected truncate result: %s", truncate("hello world", 6))
	}
}

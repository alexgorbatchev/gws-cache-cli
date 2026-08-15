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

func TestCLICommands(t *testing.T) {
	tmpDir := t.TempDir()
	testDB := filepath.Join(tmpDir, "test.db")

	// Override newClient with mock
	newClient = func() gmail.Client {
		c := &mockCLIClient{}
		c.ExecCommand = func(name string, arg ...string) ([]byte, error) {
			return []byte(`{"threads":[], "resultSizeEstimate": 0}`), nil
		}
		return c
	}

	rootCmd := NewRootCmd()
	buf := new(bytes.Buffer)
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)

	// 1. Topic Add
	rootCmd.SetArgs([]string{"--db", testDB, "topic", "add", "newsletters", "--name", "Tech Newsletters", "--query", "category:promotions"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("topic add failed: %v", err)
	}

	// Topic Add error (duplicate)
	rootCmd.SetArgs([]string{"--db", testDB, "topic", "add", "newsletters"})
	if err := rootCmd.Execute(); err == nil {
		t.Fatal("expected error adding duplicate topic")
	}

	// 2. Topic List
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "topic", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("topic list failed: %v", err)
	}
	if !strings.Contains(buf.String(), "newsletters") || !strings.Contains(buf.String(), "Tech Newsletters") {
		t.Fatalf("topic list missing added topic: %s", buf.String())
	}

	// 3. Sync Single Topic
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

	// 4. Calendar Sync Command
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "calendar", "sync", "--past", "4w", "--future", "4w"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("calendar sync failed: %v", err)
	}

	// 5. Calendar List Command
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

	// 6. Scan Inbox Command
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "scan", "--since", "all"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("scan failed: %v", err)
	}

	// 7. Search Command
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "search", "Tech", "--since", "all", "--format", "json"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("search failed: %v", err)
	}

	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "search", "--since", "all", "--format", "table"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("search table failed: %v", err)
	}

	// 8. Sync All Topics
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "sync", "--all"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("sync --all failed: %v", err)
	}

	// 9. Status
	buf.Reset()
	rootCmd.SetArgs([]string{"--db", testDB, "status"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !strings.Contains(buf.String(), "Tracked Topics:  2") {
		t.Fatalf("unexpected status output: %s", buf.String())
	}

	// 10. Export
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

	// 11. Topic Remove
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

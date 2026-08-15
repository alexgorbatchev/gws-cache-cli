package exporter

import (
	"path/filepath"
	"strings"
	"testing"

	"gws-cache/pkg/store"
)

func TestExporter(t *testing.T) {
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

	if err := db.UpsertThread("t-1", top.ID, "h-1", "Issue #42 is out!"); err != nil {
		t.Fatalf("UpsertThread failed: %v", err)
	}

	m1 := &store.Message{
		ID:           "m-1",
		ThreadID:     "t-1",
		TopicID:      top.ID,
		InternalDate: 1770000000000,
		DateISO:      "2026-02-01T10:00:00Z",
		FromAddress:  "editor@newsletter.com",
		FromName:     "Editor",
		ToAddress:    "me@example.com",
		Subject:      "Weekly Digest",
		Snippet:      "Issue #42 is out!",
		IsAutomated:  false,
	}
	if err := db.UpsertMessage(m1); err != nil {
		t.Fatalf("UpsertMessage failed: %v", err)
	}

	exp := NewExporter(db)

	// Test Markdown Export
	md, err := exp.ExportTopic("newsletters", Options{HumanOnly: true, Format: "markdown", Since: "all"})
	if err != nil {
		t.Fatalf("ExportTopic markdown failed: %v", err)
	}
	if !strings.Contains(md, "Tech Newsletters Timeline") || !strings.Contains(md, "Issue #42 is out!") {
		t.Fatalf("unexpected markdown output: %s", md)
	}

	// Test JSON Export
	js, err := exp.ExportTopic("newsletters", Options{HumanOnly: true, Format: "json", Since: "all"})
	if err != nil {
		t.Fatalf("ExportTopic json failed: %v", err)
	}
	if !strings.Contains(js, "m-1") || !strings.Contains(js, "Weekly Digest") {
		t.Fatalf("unexpected json output: %s", js)
	}

	// Test Empty Topic Markdown
	_, _ = db.CreateTopic("empty", "Empty Topic", "")
	mdEmpty, err := exp.ExportTopic("empty", Options{HumanOnly: true, Format: "markdown", Since: "all"})
	if err != nil {
		t.Fatalf("ExportTopic empty failed: %v", err)
	}
	if !strings.Contains(mdEmpty, "No messages found") {
		t.Fatalf("unexpected empty markdown output: %s", mdEmpty)
	}

	// Test Nonexistent Topic
	_, err = exp.ExportTopic("nonexistent", Options{HumanOnly: true, Format: "markdown", Since: "all"})
	if err == nil {
		t.Fatal("expected error on nonexistent topic export")
	}
}

func TestCleanText(t *testing.T) {
	input := "Hello\nWorld&#39;s &gt; &lt; &amp; test\r"
	expected := "Hello World's > < & test"
	got := cleanText(input)
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

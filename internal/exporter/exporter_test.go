package exporter

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexgorbatchev/gws-cache-cli/internal/store"
	"github.com/alexgorbatchev/gws-cache-cli/internal/timewindow"
)

// newTestStore opens an empty cache seeded with one topic holding one message.
func newTestStore(t *testing.T) *store.DB {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	top, err := db.CreateTopic("newsletters", "Tech Newsletters", "category:promotions")
	if err != nil {
		t.Fatalf("creating topic: %v", err)
	}
	if err := db.UpsertThread("t-1", &top.ID, "h-1", "Issue #42 is out!"); err != nil {
		t.Fatalf("upserting thread: %v", err)
	}
	if err := db.UpsertMessage(&store.Message{
		ID:           "m-1",
		ThreadID:     "t-1",
		TopicID:      &top.ID,
		InternalDate: 1770000000000,
		DateISO:      "2026-02-01T10:00:00Z",
		FromAddress:  "editor@newsletter.example",
		FromName:     "Editor",
		ToAddress:    "me@example.com",
		Subject:      "Weekly Digest",
		Snippet:      "Issue #42 is out!",
	}); err != nil {
		t.Fatalf("upserting message: %v", err)
	}
	if _, err := db.CreateTopic("empty", "Empty Topic", ""); err != nil {
		t.Fatalf("creating empty topic: %v", err)
	}

	return db
}

func unbounded(t *testing.T) timewindow.Window {
	t.Helper()
	w, err := timewindow.Parse("all")
	if err != nil {
		t.Fatalf("parsing window: %v", err)
	}
	return w
}

func TestExportTopic(t *testing.T) {
	db := newTestStore(t)
	exp := NewExporter(db)

	tests := []struct {
		name        string
		slug        string
		format      Format
		wantStrings []string
		wantErr     bool
	}{
		{
			name:        "markdown timeline",
			slug:        "newsletters",
			format:      FormatMarkdown,
			wantStrings: []string{"Tech Newsletters Timeline", "Issue #42 is out!", "Feb 01, 2026"},
		},
		{
			name:        "json timeline",
			slug:        "newsletters",
			format:      FormatJSON,
			wantStrings: []string{"m-1", "Weekly Digest"},
		},
		{
			name:        "format is case insensitive",
			slug:        "newsletters",
			format:      "JSON",
			wantStrings: []string{`"id": "m-1"`},
		},
		{
			name:        "unknown format falls back to markdown",
			slug:        "newsletters",
			format:      "rtf",
			wantStrings: []string{"Tech Newsletters Timeline"},
		},
		{
			name:        "empty topic",
			slug:        "empty",
			format:      FormatMarkdown,
			wantStrings: []string{"No messages found"},
		},
		{
			name:    "unknown topic",
			slug:    "nonexistent",
			format:  FormatMarkdown,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := exp.ExportTopic(tt.slug, Options{HumanOnly: true, Format: tt.format, Since: unbounded(t)})
			if (err != nil) != tt.wantErr {
				t.Fatalf("ExportTopic(%q) error = %v, wantErr %v", tt.slug, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			for _, want := range tt.wantStrings {
				if !strings.Contains(got, want) {
					t.Fatalf("ExportTopic(%q) output %q missing %q", tt.slug, got, want)
				}
			}
		})
	}
}

func TestExportTopicHonoursWindow(t *testing.T) {
	db := newTestStore(t)

	recent, err := timewindow.Parse("1d")
	if err != nil {
		t.Fatalf("parsing window: %v", err)
	}

	got, err := NewExporter(db).ExportTopic("newsletters", Options{Format: FormatMarkdown, Since: recent})
	if err != nil {
		t.Fatalf("ExportTopic unexpected error: %v", err)
	}
	if !strings.Contains(got, "No messages found") {
		t.Fatalf("a one day window should exclude the 2026-02-01 message, got %q", got)
	}
}

func TestCleanText(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "newlines collapse", input: "Hello\nWorld\r", want: "Hello World"},
		{name: "html entities decode", input: "&#39;s &gt; &lt; &amp;", want: "'s > < &"},
		{name: "surrounding space trimmed", input: "  padded  ", want: "padded"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cleanText(tt.input); got != tt.want {
				t.Fatalf("cleanText(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

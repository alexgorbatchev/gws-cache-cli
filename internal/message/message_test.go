package message

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/alexgorbatchev/gws-cache-cli/internal/classifier"
	"github.com/alexgorbatchev/gws-cache-cli/internal/gws"
	"github.com/alexgorbatchev/gws-cache-cli/internal/store"
)

var (
	reference  = time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	topicOne   = int64(1)
	topicSeven = int64(7)
)

func TestParseAddress(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		wantAddr string
		wantName string
	}{
		{name: "empty", header: "", wantAddr: "", wantName: ""},
		{name: "bare address", header: "alex@example.com", wantAddr: "alex@example.com"},
		{name: "angle addressed", header: "Alex Gorbatchev <alex@example.com>", wantAddr: "alex@example.com", wantName: "Alex Gorbatchev"},
		{name: "quoted display name", header: `"Gorbatchev, Alex" <alex@example.com>`, wantAddr: "alex@example.com", wantName: "Gorbatchev, Alex"},
		{name: "display name containing angle bracket", header: `"a < b" <alex@example.com>`, wantAddr: "alex@example.com", wantName: "a < b"},
		{name: "encoded word display name", header: "=?utf-8?q?Andr=C3=A9?= <andre@example.com>", wantAddr: "andre@example.com", wantName: "André"},
		{name: "unparseable falls back to raw", header: "not an address at all", wantAddr: "not an address at all"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, name := ParseAddress(tt.header)
			if addr != tt.wantAddr {
				t.Fatalf("ParseAddress(%q) address = %q, want %q", tt.header, addr, tt.wantAddr)
			}
			if name != tt.wantName {
				t.Fatalf("ParseAddress(%q) name = %q, want %q", tt.header, name, tt.wantName)
			}
		})
	}
}

func TestISODate(t *testing.T) {
	tests := []struct {
		name         string
		dateHeader   string
		internalDate int64
		want         string
	}{
		{
			name:         "internal date wins",
			dateHeader:   "Mon, 01 Jun 2026 10:00:00 +0000",
			internalDate: time.Date(2026, 7, 4, 9, 30, 0, 0, time.UTC).UnixMilli(),
			want:         "2026-07-04T09:30:00Z",
		},
		{
			name:       "falls back to RFC1123Z header",
			dateHeader: "Mon, 01 Jun 2026 10:00:00 +0000",
			want:       "2026-06-01T10:00:00Z",
		},
		{
			name:       "falls back to RFC5322 header without day name",
			dateHeader: "1 Jun 2026 10:00:00 +0000",
			want:       "2026-06-01T10:00:00Z",
		},
		{
			name:       "unparseable header falls back to now",
			dateHeader: "whenever",
			want:       reference.Format(time.RFC3339),
		},
		{
			name: "missing header falls back to now",
			want: reference.Format(time.RFC3339),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ISODate(tt.dateHeader, tt.internalDate, reference)
			if got != tt.want {
				t.Fatalf("ISODate(%q, %d) = %q, want %q", tt.dateHeader, tt.internalDate, got, tt.want)
			}
		})
	}
}

func TestFromDetail(t *testing.T) {
	detail := gws.MessageDetail{
		ID:           "m1",
		ThreadID:     "t1",
		InternalDate: "1780000000000",
		Snippet:      "Quarterly summary attached",
		Payload: gws.Payload{
			Headers: []gws.Header{
				{Name: "From", Value: "Alex Gorbatchev <alex@example.com>"},
				{Name: "To", Value: "team@example.com"},
				{Name: "Subject", Value: "Quarterly summary"},
				{Name: "Date", Value: "Mon, 01 Jun 2026 10:00:00 +0000"},
			},
		},
	}

	got, err := NewConverter(classifier.Default()).FromDetail(detail, "t1", &topicSeven, reference)
	if err != nil {
		t.Fatalf("FromDetail unexpected error: %v", err)
	}
	if got.ID != "m1" || got.ThreadID != "t1" || got.TopicID == nil || *got.TopicID != 7 {
		t.Fatalf("FromDetail identity = %+v", got)
	}
	if got.InternalDate != 1780000000000 {
		t.Fatalf("FromDetail InternalDate = %d, want 1780000000000", got.InternalDate)
	}
	if got.FromAddress != "alex@example.com" || got.FromName != "Alex Gorbatchev" {
		t.Fatalf("FromDetail sender = %q / %q", got.FromAddress, got.FromName)
	}
	if got.Subject != "Quarterly summary" || got.ToAddress != "team@example.com" {
		t.Fatalf("FromDetail headers = %+v", got)
	}
	if got.IsAutomated {
		t.Fatalf("FromDetail classified a human message as automated: %q", got.ClassificationReason)
	}
}

func TestFromDetailRejectsUnparseableInternalDate(t *testing.T) {
	detail := gws.MessageDetail{ID: "m1", InternalDate: "not-a-number"}

	if _, err := NewConverter(classifier.Default()).FromDetail(detail, "t1", &topicOne, reference); err == nil {
		t.Fatal("FromDetail should reject an unparseable internalDate instead of silently storing epoch zero")
	}
}

func TestIngestThread(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "ingest.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	defer db.Close()

	top, err := db.CreateTopic("news", "News", "q")
	if err != nil {
		t.Fatalf("creating topic: %v", err)
	}

	detail := &gws.ThreadDetail{
		ID:        "t-1",
		HistoryID: "100",
		Messages: []gws.MessageDetail{
			{ID: "m-1", InternalDate: "1780000000000", Snippet: "first"},
			{ID: "m-2", InternalDate: "1790000000000", Snippet: "second"},
		},
	}

	stats, err := NewIngestor(db, classifier.Default()).IngestThread(detail, &top.ID, "100", "snippet", reference)
	if err != nil {
		t.Fatalf("IngestThread unexpected error: %v", err)
	}
	if stats.MessagesIngested != 2 {
		t.Fatalf("MessagesIngested = %d, want 2", stats.MessagesIngested)
	}
	if stats.MaxInternalDate != 1790000000000 {
		t.Fatalf("MaxInternalDate = %d, want the newest message date", stats.MaxInternalDate)
	}

	stored, err := db.ListMessagesByTopic(top.ID, false, 0)
	if err != nil {
		t.Fatalf("listing messages: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("stored %d messages, want 2", len(stored))
	}
}

func TestIngestThreadReportsBadMessage(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "ingest.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	defer db.Close()

	top, err := db.CreateTopic("news", "News", "q")
	if err != nil {
		t.Fatalf("creating topic: %v", err)
	}

	detail := &gws.ThreadDetail{
		ID:       "t-1",
		Messages: []gws.MessageDetail{{ID: "m-1", InternalDate: "nonsense"}},
	}

	if _, err := NewIngestor(db, classifier.Default()).IngestThread(detail, &top.ID, "1", "s", reference); err == nil {
		t.Fatal("IngestThread should report a message it cannot parse")
	}
}

func TestIngestThreadReportsStoreFailure(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	db.Close()

	detail := &gws.ThreadDetail{ID: "t-1"}
	if _, err := NewIngestor(db, classifier.Default()).IngestThread(detail, &topicOne, "1", "s", reference); err == nil {
		t.Fatal("IngestThread should report a store failure")
	}
}

func TestFromDetailAcceptsMissingInternalDate(t *testing.T) {
	detail := gws.MessageDetail{ID: "m1"}

	got, err := NewConverter(classifier.Default()).FromDetail(detail, "t1", &topicOne, reference)
	if err != nil {
		t.Fatalf("FromDetail unexpected error: %v", err)
	}
	if got.InternalDate != 0 {
		t.Fatalf("FromDetail InternalDate = %d, want 0", got.InternalDate)
	}
}

package topic

import (
	"path/filepath"
	"testing"

	"gws-cache/pkg/store"
)

func TestTopicService(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer db.Close()

	svc := NewService(db)

	// 1. Register Topic
	top, err := svc.RegisterTopic("newsletters", "Tech Newsletters", "category:promotions")
	if err != nil {
		t.Fatalf("RegisterTopic failed: %v", err)
	}
	if top.Slug != "newsletters" || top.DisplayName != "Tech Newsletters" || top.Query != "category:promotions" {
		t.Fatalf("unexpected topic data: %+v", top)
	}

	// 2. Register Topic with empty display name
	top2, err := svc.RegisterTopic("billing", "", "from:billing@")
	if err != nil {
		t.Fatalf("RegisterTopic with empty display name failed: %v", err)
	}
	if top2.DisplayName != "billing" {
		t.Fatalf("expected display name 'billing', got %q", top2.DisplayName)
	}

	// 3. Get Topic
	got, err := svc.GetTopic("newsletters")
	if err != nil {
		t.Fatalf("GetTopic failed: %v", err)
	}
	if got.Slug != "newsletters" {
		t.Fatalf("expected slug 'newsletters', got %q", got.Slug)
	}

	// 4. List Topics
	topics, err := svc.ListTopics()
	if err != nil {
		t.Fatalf("ListTopics failed: %v", err)
	}
	if len(topics) != 2 {
		t.Fatalf("expected 2 topics, got %d", len(topics))
	}

	// 5. Delete Topic
	if err := svc.DeleteTopic("newsletters"); err != nil {
		t.Fatalf("DeleteTopic failed: %v", err)
	}

	if _, err := svc.GetTopic("newsletters"); err == nil {
		t.Fatal("expected error getting deleted topic")
	}
}

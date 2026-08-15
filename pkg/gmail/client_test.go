package gmail

import (
	"errors"
	"testing"
)

func TestNewCLIClient(t *testing.T) {
	client := NewCLIClient()
	if client == nil || client.ExecCommand == nil {
		t.Fatal("expected non-nil CLIClient and ExecCommand")
	}
}

func TestCLIClient_ListThreads(t *testing.T) {
	client := NewCLIClient()
	client.ExecCommand = func(name string, arg ...string) ([]byte, error) {
		if name != "gws" {
			return nil, errors.New("unexpected binary name")
		}
		jsonResp := `{
			"threads": [
				{"id": "t1", "historyId": "100", "snippet": "Snippet 1"},
				{"id": "t2", "historyId": "101", "snippet": "Snippet 2"}
			],
			"resultSizeEstimate": 2
		}`
		return []byte(jsonResp), nil
	}

	threads, err := client.ListThreads("Envoy")
	if err != nil {
		t.Fatalf("ListThreads failed: %v", err)
	}
	if len(threads) != 2 {
		t.Fatalf("expected 2 threads, got %d", len(threads))
	}
	if threads[0].ID != "t1" || threads[1].ID != "t2" {
		t.Fatalf("unexpected thread IDs: %+v", threads)
	}
}

func TestCLIClient_ListThreads_Error(t *testing.T) {
	client := NewCLIClient()
	client.ExecCommand = func(name string, arg ...string) ([]byte, error) {
		return nil, errors.New("command failed")
	}

	_, err := client.ListThreads("Envoy")
	if err == nil {
		t.Fatal("expected error on command failure")
	}
}

func TestCLIClient_ListThreads_InvalidJSON(t *testing.T) {
	client := NewCLIClient()
	client.ExecCommand = func(name string, arg ...string) ([]byte, error) {
		return []byte("invalid json"), nil
	}

	_, err := client.ListThreads("Envoy")
	if err == nil {
		t.Fatal("expected error on invalid json")
	}
}

func TestCLIClient_GetThread(t *testing.T) {
	client := NewCLIClient()
	client.ExecCommand = func(name string, arg ...string) ([]byte, error) {
		jsonResp := `{
			"id": "t1",
			"historyId": "100",
			"messages": [
				{
					"id": "m1",
					"threadId": "t1",
					"historyId": "100",
					"internalDate": "1770000000000",
					"snippet": "Test snippet",
					"payload": {
						"headers": [
							{"name": "Date", "value": "Mon, 20 Jul 2026 13:00:00 -0700"},
							{"name": "From", "value": "Leither Moise <leither@envoy.com>"}
						]
					}
				}
			]
		}`
		return []byte(jsonResp), nil
	}

	detail, err := client.GetThread("t1")
	if err != nil {
		t.Fatalf("GetThread failed: %v", err)
	}
	if detail.ID != "t1" || len(detail.Messages) != 1 {
		t.Fatalf("unexpected detail: %+v", detail)
	}

	msg := detail.Messages[0]
	if msg.GetHeader("Date") != "Mon, 20 Jul 2026 13:00:00 -0700" {
		t.Fatalf("unexpected Date header: %s", msg.GetHeader("Date"))
	}
	if msg.GetHeader("FROM") != "Leither Moise <leither@envoy.com>" {
		t.Fatalf("unexpected From header with case insensitive lookup: %s", msg.GetHeader("FROM"))
	}
	if msg.GetHeader("NonExistent") != "" {
		t.Fatalf("expected empty for nonexistent header")
	}

	hMap := msg.HeaderMap()
	if hMap["From"] != "Leither Moise <leither@envoy.com>" {
		t.Fatalf("unexpected HeaderMap result: %+v", hMap)
	}
}

func TestCLIClient_GetThread_Error(t *testing.T) {
	client := NewCLIClient()
	client.ExecCommand = func(name string, arg ...string) ([]byte, error) {
		return nil, errors.New("gws error")
	}

	_, err := client.GetThread("t1")
	if err == nil {
		t.Fatal("expected error on command failure")
	}
}

func TestCLIClient_GetThread_InvalidJSON(t *testing.T) {
	client := NewCLIClient()
	client.ExecCommand = func(name string, arg ...string) ([]byte, error) {
		return []byte("invalid json"), nil
	}

	_, err := client.GetThread("t1")
	if err == nil {
		t.Fatal("expected error on invalid json")
	}
}

func TestCLIClient_ListCalendarEvents(t *testing.T) {
	client := NewCLIClient()
	client.ExecCommand = func(name string, arg ...string) ([]byte, error) {
		jsonResp := `{
			"kind": "calendar#events",
			"nextSyncToken": "token-123",
			"items": [
				{"id": "e1", "summary": "Interview"}
			]
		}`
		return []byte(jsonResp), nil
	}

	resp, err := client.ListCalendarEvents(CalendarListParams{CalendarID: "primary"})
	if err != nil {
		t.Fatalf("ListCalendarEvents failed: %v", err)
	}
	if resp.NextSyncToken != "token-123" || len(resp.Items) != 1 {
		t.Fatalf("unexpected calendar response: %+v", resp)
	}

	client.ExecCommand = func(name string, arg ...string) ([]byte, error) {
		return nil, errors.New("command error")
	}
	_, err = client.ListCalendarEvents(CalendarListParams{})
	if err == nil {
		t.Fatal("expected error on command failure")
	}

	client.ExecCommand = func(name string, arg ...string) ([]byte, error) {
		return []byte("invalid json"), nil
	}
	_, err = client.ListCalendarEvents(CalendarListParams{})
	if err == nil {
		t.Fatal("expected error on invalid json")
	}
}

func TestEqualIgnoreCase(t *testing.T) {
	if !equalIgnoreCase("Subject", "subject") {
		t.Fatal("expected Subject == subject")
	}
	if equalIgnoreCase("Subject", "Subj") {
		t.Fatal("expected false for different lengths")
	}
	if equalIgnoreCase("Subject", "Objects") {
		t.Fatal("expected false for different strings")
	}
}

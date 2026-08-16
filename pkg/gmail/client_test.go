package gmail

import (
	"errors"
	"strings"
	"testing"
)

func TestNewCLIClient(t *testing.T) {
	c := NewCLIClient()
	if c == nil || c.ExecCommand == nil {
		t.Fatal("expected non-nil CLIClient with ExecCommand")
	}

	out, err := c.ExecCommand("echo", "hello")
	if err != nil {
		t.Fatalf("ExecCommand failed: %v", err)
	}
	if !strings.Contains(string(out), "hello") {
		t.Fatalf("unexpected ExecCommand output: %s", string(out))
	}
}

func TestCLIClient_ListThreads(t *testing.T) {
	c := &CLIClient{
		ExecCommand: func(name string, arg ...string) ([]byte, error) {
			return []byte(`{"threads":[{"id":"t1","historyId":"100","snippet":"test"}]}`), nil
		},
	}

	threads, err := c.ListThreads("query")
	if err != nil {
		t.Fatalf("ListThreads failed: %v", err)
	}
	if len(threads) != 1 || threads[0].ID != "t1" {
		t.Fatalf("unexpected threads: %+v", threads)
	}
}

func TestCLIClient_ListThreads_Error(t *testing.T) {
	c := &CLIClient{
		ExecCommand: func(name string, arg ...string) ([]byte, error) {
			return nil, errors.New("exec error")
		},
	}

	_, err := c.ListThreads("query")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCLIClient_ListThreads_InvalidJSON(t *testing.T) {
	c := &CLIClient{
		ExecCommand: func(name string, arg ...string) ([]byte, error) {
			return []byte(`invalid json`), nil
		},
	}

	_, err := c.ListThreads("query")
	if err == nil {
		t.Fatal("expected error on invalid json")
	}
}

func TestCLIClient_GetThread(t *testing.T) {
	c := &CLIClient{
		ExecCommand: func(name string, arg ...string) ([]byte, error) {
			return []byte(`{"id":"t1","historyId":"100","messages":[]}`), nil
		},
	}

	thread, err := c.GetThread("t1")
	if err != nil {
		t.Fatalf("GetThread failed: %v", err)
	}
	if thread.ID != "t1" {
		t.Fatalf("unexpected thread: %+v", thread)
	}
}

func TestCLIClient_GetThread_Error(t *testing.T) {
	c := &CLIClient{
		ExecCommand: func(name string, arg ...string) ([]byte, error) {
			return nil, errors.New("exec error")
		},
	}

	_, err := c.GetThread("t1")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCLIClient_GetThread_InvalidJSON(t *testing.T) {
	c := &CLIClient{
		ExecCommand: func(name string, arg ...string) ([]byte, error) {
			return []byte(`invalid json`), nil
		},
	}

	_, err := c.GetThread("t1")
	if err == nil {
		t.Fatal("expected error on invalid json")
	}
}

func TestCLIClient_ListCalendarEvents(t *testing.T) {
	c := &CLIClient{
		ExecCommand: func(name string, arg ...string) ([]byte, error) {
			return []byte(`{"kind":"calendar#events","items":[]}`), nil
		},
	}

	resp, err := c.ListCalendarEvents(CalendarListParams{CalendarID: ""})
	if err != nil {
		t.Fatalf("ListCalendarEvents failed: %v", err)
	}
	if resp.Kind != "calendar#events" {
		t.Fatalf("unexpected response: %+v", resp)
	}

	// Exec error branch
	cErr := &CLIClient{
		ExecCommand: func(name string, arg ...string) ([]byte, error) {
			return nil, errors.New("calendar exec error")
		},
	}
	_, err = cErr.ListCalendarEvents(CalendarListParams{})
	if err == nil {
		t.Fatal("expected error on exec error")
	}

	// Invalid json branch
	cJSONErr := &CLIClient{
		ExecCommand: func(name string, arg ...string) ([]byte, error) {
			return []byte(`invalid json`), nil
		},
	}
	_, err = cJSONErr.ListCalendarEvents(CalendarListParams{})
	if err == nil {
		t.Fatal("expected error on invalid json")
	}
}

func TestMessageDetail_Headers(t *testing.T) {
	msg := MessageDetail{
		Payload: Payload{
			Headers: []Header{
				{Name: "Subject", Value: "Hello World"},
				{Name: "From", Value: "test@example.com"},
			},
		},
	}

	if msg.GetHeader("Subject") != "Hello World" {
		t.Fatalf("expected 'Hello World', got %q", msg.GetHeader("Subject"))
	}
	if msg.GetHeader("nonexistent") != "" {
		t.Fatalf("expected empty string for nonexistent header")
	}

	hMap := msg.HeaderMap()
	if hMap["Subject"] != "Hello World" || hMap["From"] != "test@example.com" {
		t.Fatalf("unexpected header map: %+v", hMap)
	}
}

func TestEqualIgnoreCase(t *testing.T) {
	if !equalIgnoreCase("Subject", "subject") {
		t.Fatal("expected equal ignore case")
	}
	if equalIgnoreCase("a", "bb") {
		t.Fatal("expected false for different lengths")
	}
	if equalIgnoreCase("a", "b") {
		t.Fatal("expected false for different characters")
	}
}

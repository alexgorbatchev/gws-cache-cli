package gmail

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
)

type ThreadSummary struct {
	ID        string `json:"id"`
	HistoryID string `json:"historyId"`
	Snippet   string `json:"snippet"`
}

type ThreadListResponse struct {
	Threads            []ThreadSummary `json:"threads"`
	ResultSizeEstimate int             `json:"resultSizeEstimate"`
	NextPageToken      string          `json:"nextPageToken"`
}

type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type Payload struct {
	Headers []Header `json:"headers"`
	Body    struct {
		Data string `json:"data"`
	} `json:"body"`
}

type MessageDetail struct {
	ID           string  `json:"id"`
	ThreadID     string  `json:"threadId"`
	HistoryID    string  `json:"historyId"`
	InternalDate string  `json:"internalDate"`
	Snippet      string  `json:"snippet"`
	Payload      Payload `json:"payload"`
}

type ThreadDetail struct {
	ID        string          `json:"id"`
	HistoryID string          `json:"historyId"`
	Messages  []MessageDetail `json:"messages"`
}

type CalendarAttendee struct {
	Email          string `json:"email"`
	DisplayName    string `json:"displayName"`
	ResponseStatus string `json:"responseStatus"`
}

type CalendarEventDetail struct {
	ID          string `json:"id"`
	Status      string `json:"status"` // "confirmed", "tentative", "cancelled"
	Summary     string `json:"summary"`
	Description string `json:"description"`
	Location    string `json:"location"`
	HTMLLink    string `json:"htmlLink"`
	ICalUID     string `json:"iCalUID"`
	Creator     struct {
		Email string `json:"email"`
	} `json:"creator"`
	Organizer struct {
		Email string `json:"email"`
	} `json:"organizer"`
	Start struct {
		DateTime string `json:"dateTime"`
		Date     string `json:"date"`
	} `json:"start"`
	End struct {
		DateTime string `json:"dateTime"`
		Date     string `json:"date"`
	} `json:"end"`
	Attendees []CalendarAttendee `json:"attendees"`
}

type CalendarEventsResponse struct {
	Kind          string                `json:"kind"`
	NextSyncToken string                `json:"nextSyncToken"`
	NextPageToken string                `json:"nextPageToken"`
	Items         []CalendarEventDetail `json:"items"`
}

type CalendarListParams struct {
	CalendarID   string `json:"calendarId"`
	TimeMin      string `json:"timeMin,omitempty"`
	TimeMax      string `json:"timeMax,omitempty"`
	SyncToken    string `json:"syncToken,omitempty"`
	SingleEvents bool   `json:"singleEvents"`
}

type Client interface {
	ListThreads(query string) ([]ThreadSummary, error)
	GetThread(threadID string) (*ThreadDetail, error)
	ListCalendarEvents(params CalendarListParams) (*CalendarEventsResponse, error)
}

type CLIClient struct {
	ExecCommand func(name string, arg ...string) ([]byte, error)
}

func NewCLIClient() *CLIClient {
	return &CLIClient{
		ExecCommand: func(name string, arg ...string) ([]byte, error) {
			cmd := exec.Command(name, arg...)
			return cmd.Output()
		},
	}
}

func (c *CLIClient) ListThreads(query string) ([]ThreadSummary, error) {
	params := fmt.Sprintf(`{"userId": "me", "q": %q}`, query)
	out, err := c.ExecCommand("gws", "gmail", "users", "threads", "list", "--params", params)
	if err != nil {
		return nil, fmt.Errorf("executing gws threads list: %w", err)
	}

	var resp ThreadListResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("unmarshaling threads list response: %w", err)
	}

	return resp.Threads, nil
}

func (c *CLIClient) GetThread(threadID string) (*ThreadDetail, error) {
	params := fmt.Sprintf(`{"userId": "me", "id": %q}`, threadID)
	out, err := c.ExecCommand("gws", "gmail", "users", "threads", "get", "--params", params)
	if err != nil {
		return nil, fmt.Errorf("executing gws threads get: %w", err)
	}

	var detail ThreadDetail
	if err := json.Unmarshal(out, &detail); err != nil {
		return nil, fmt.Errorf("unmarshaling thread detail response: %w", err)
	}

	return &detail, nil
}

func (c *CLIClient) ListCalendarEvents(p CalendarListParams) (*CalendarEventsResponse, error) {
	if p.CalendarID == "" {
		p.CalendarID = "primary"
	}
	p.SingleEvents = true

	b, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("marshaling calendar params: %w", err)
	}

	out, err := c.ExecCommand("gws", "calendar", "events", "list", "--params", string(b))
	if err != nil {
		return nil, fmt.Errorf("executing gws calendar events list: %w", err)
	}

	var resp CalendarEventsResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("unmarshaling calendar events list response: %w", err)
	}

	return &resp, nil
}

// GetHeader returns the value of a header by case-insensitive name match.
func (m *MessageDetail) GetHeader(name string) string {
	for _, h := range m.Payload.Headers {
		if equalIgnoreCase(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// HeaderMap converts headers into a map with canonicalized HTTP header keys.
func (m *MessageDetail) HeaderMap() map[string]string {
	res := make(map[string]string)
	for _, h := range m.Payload.Headers {
		canonicalKey := http.CanonicalHeaderKey(h.Name)
		res[canonicalKey] = h.Value
	}
	return res
}

func equalIgnoreCase(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// Package gws wraps the Google Workspace (`gws`) command line tool, which is
// how this cache reaches the Gmail and Google Calendar APIs.
package gws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
)

// binaryName is the executable resolved on PATH for every call.
const binaryName = "gws"

// DefaultCalendarID is the calendar used when a request names none.
const DefaultCalendarID = "primary"

// maxPages bounds pagination so that a server returning an endless stream of
// page tokens cannot spin forever. At Gmail's 100 results per page this is far
// more mail than any window a cache would ask for.
const maxPages = 200

// errTooManyPages reports that pagination did not terminate. It is a sentinel
// so callers can tell it apart from a transport failure.
var errTooManyPages = errors.New("pagination did not terminate within the page limit")

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
	PageToken    string `json:"pageToken,omitempty"`
	SingleEvents bool   `json:"singleEvents"`
}

// Client invokes the `gws` executable. Its zero value is ready to use.
type Client struct{}

// New returns a Client. Callers control cancellation and deadlines through the
// context they pass to each call.
func New() *Client {
	return &Client{}
}

// threadListParams is the request body for a Gmail thread listing.
type threadListParams struct {
	UserID    string `json:"userId"`
	Query     string `json:"q"`
	PageToken string `json:"pageToken,omitempty"`
}

// threadGetParams is the request body for a single Gmail thread.
type threadGetParams struct {
	UserID string `json:"userId"`
	ID     string `json:"id"`
}

// ListThreads returns every thread summary matching a Gmail search query,
// following pagination to the end. Gmail returns 100 threads per page by
// default, so a caller that read only the first page would silently see a
// truncated mailbox.
func (c *Client) ListThreads(ctx context.Context, query string) ([]ThreadSummary, error) {
	var (
		threads   []ThreadSummary
		pageToken string
	)

	for page := 0; page < maxPages; page++ {
		params, err := json.Marshal(threadListParams{UserID: "me", Query: query, PageToken: pageToken})
		if err != nil {
			return nil, fmt.Errorf("encoding thread list parameters: %w", err)
		}

		out, err := c.run(ctx, "gmail", "users", "threads", "list", "--params", string(params))
		if err != nil {
			return nil, fmt.Errorf("listing threads for query %q: %w", query, err)
		}

		var resp ThreadListResponse
		if err := json.Unmarshal(out, &resp); err != nil {
			return nil, fmt.Errorf("parsing thread list response: %w", err)
		}

		threads = append(threads, resp.Threads...)

		if resp.NextPageToken == "" || resp.NextPageToken == pageToken {
			return threads, nil
		}
		pageToken = resp.NextPageToken
	}

	return nil, fmt.Errorf("listing threads for query %q: %w", query, errTooManyPages)
}

// GetThread returns the full detail of a single Gmail thread.
func (c *Client) GetThread(ctx context.Context, threadID string) (*ThreadDetail, error) {
	params, err := json.Marshal(threadGetParams{UserID: "me", ID: threadID})
	if err != nil {
		return nil, fmt.Errorf("encoding thread parameters: %w", err)
	}

	out, err := c.run(ctx, "gmail", "users", "threads", "get", "--params", string(params))
	if err != nil {
		return nil, fmt.Errorf("getting thread %s: %w", threadID, err)
	}

	var detail ThreadDetail
	if err := json.Unmarshal(out, &detail); err != nil {
		return nil, fmt.Errorf("parsing thread %s: %w", threadID, err)
	}
	return &detail, nil
}

// ListCalendarEvents returns every calendar event for a window or a sync token,
// following pagination to the end. Google returns 250 events per page by
// default and only issues the next sync token on the final page, so stopping
// early would both truncate the results and lose the token that makes the next
// sync incremental.
func (c *Client) ListCalendarEvents(ctx context.Context, p CalendarListParams) (*CalendarEventsResponse, error) {
	if p.CalendarID == "" {
		p.CalendarID = DefaultCalendarID
	}
	// Recurring events are expanded into instances so that cached rows map
	// one-to-one onto what a user sees in their calendar.
	p.SingleEvents = true

	combined := &CalendarEventsResponse{}

	for page := 0; page < maxPages; page++ {
		params, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("encoding calendar parameters: %w", err)
		}

		out, err := c.run(ctx, "calendar", "events", "list", "--params", string(params))
		if err != nil {
			return nil, fmt.Errorf("listing events for calendar %s: %w", p.CalendarID, err)
		}

		var resp CalendarEventsResponse
		if err := json.Unmarshal(out, &resp); err != nil {
			return nil, fmt.Errorf("parsing calendar events response: %w", err)
		}

		combined.Kind = resp.Kind
		combined.Items = append(combined.Items, resp.Items...)

		if resp.NextPageToken == "" || resp.NextPageToken == p.PageToken {
			// The final page carries the token for the next incremental sync.
			combined.NextSyncToken = resp.NextSyncToken
			return combined, nil
		}
		p.PageToken = resp.NextPageToken
	}

	return nil, fmt.Errorf("listing events for calendar %s: %w", p.CalendarID, errTooManyPages)
}

// run executes the gws binary and returns its stdout. Anything the command
// wrote to stderr is folded into the error, because that is where gws reports
// authentication and quota problems the user needs to act on.
func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, binaryName, args...).Output()
	if err == nil {
		return out, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
			return nil, fmt.Errorf("%s %s: %w: %s", binaryName, strings.Join(args[:3], " "), err, stderr)
		}
	}
	return nil, fmt.Errorf("running %s: %w", binaryName, err)
}

// GetHeader returns the value of a header by case-insensitive name match.
func (m *MessageDetail) GetHeader(name string) string {
	for _, h := range m.Payload.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// HeaderMap converts headers into a map with canonicalized header keys so
// callers can look them up by their standard spelling.
func (m *MessageDetail) HeaderMap() map[string]string {
	headers := make(map[string]string, len(m.Payload.Headers))
	for _, h := range m.Payload.Headers {
		headers[http.CanonicalHeaderKey(h.Name)] = h.Value
	}
	return headers
}

package gws

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeGWS installs a stand-in `gws` executable on PATH for the duration of the
// test. Driving the real exec path this way keeps the production code free of
// an injected command seam that exists only for tests.
func fakeGWS(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake executable is a POSIX shell script")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, binaryName)
	body := "#!/bin/sh\n" + script + "\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("writing fake gws: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// echoArgs makes the fake record the arguments it was invoked with so tests can
// assert on the parameters handed to the real CLI.
func echoArgs(t *testing.T, stdout string) string {
	t.Helper()
	argsFile := filepath.Join(t.TempDir(), "args.txt")
	fakeGWS(t, fmt.Sprintf(`printf '%%s\n' "$@" > %s
cat <<'JSON'
%s
JSON`, argsFile, stdout))
	return argsFile
}

func TestListThreads(t *testing.T) {
	argsFile := echoArgs(t, `{"threads":[{"id":"t1","historyId":"100","snippet":"hello"}]}`)

	threads, err := New().ListThreads(t.Context(), "category:promotions")
	if err != nil {
		t.Fatalf("ListThreads unexpected error: %v", err)
	}
	if len(threads) != 1 || threads[0].ID != "t1" || threads[0].HistoryID != "100" {
		t.Fatalf("ListThreads = %+v", threads)
	}

	recorded, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("reading recorded args: %v", err)
	}
	args := string(recorded)
	for _, want := range []string{"gmail", "users", "threads", "list", "--params", `"q":"category:promotions"`} {
		if !strings.Contains(args, want) {
			t.Fatalf("invocation %q missing %q", args, want)
		}
	}
}

// pagedGWS installs a fake that replies with a different canned page on each
// invocation, so pagination can be driven end to end.
func pagedGWS(t *testing.T, pages ...string) string {
	t.Helper()

	dir := t.TempDir()
	counter := filepath.Join(dir, "calls")

	var cases strings.Builder
	for i, page := range pages {
		cases.WriteString(fmt.Sprintf("  %d) cat <<'JSON'\n%s\nJSON\n  ;;\n", i+1, page))
	}

	fakeGWS(t, fmt.Sprintf(`n=$(cat %s 2>/dev/null || echo 0)
n=$((n+1))
echo "$n" > %s
case "$n" in
%sesac`, counter, counter, cases.String()))

	return counter
}

func callCount(t *testing.T, counter string) int {
	t.Helper()
	raw, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("reading call counter: %v", err)
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &n); err != nil {
		t.Fatalf("parsing call counter %q: %v", raw, err)
	}
	return n
}

func TestListThreadsFollowsPagination(t *testing.T) {
	// Gmail returns 100 threads per page by default; reading only the first
	// page would silently truncate the mailbox.
	counter := pagedGWS(t,
		`{"threads":[{"id":"t1"},{"id":"t2"}],"nextPageToken":"p2"}`,
		`{"threads":[{"id":"t3"}],"nextPageToken":"p3"}`,
		`{"threads":[{"id":"t4"}]}`,
	)

	threads, err := New().ListThreads(t.Context(), "q")
	if err != nil {
		t.Fatalf("ListThreads unexpected error: %v", err)
	}
	if len(threads) != 4 {
		t.Fatalf("ListThreads returned %d threads, want all 4 across three pages", len(threads))
	}
	if got := callCount(t, counter); got != 3 {
		t.Fatalf("gws was called %d times, want one call per page", got)
	}
}

func TestListThreadsStopsOnRepeatedPageToken(t *testing.T) {
	// A server that keeps handing back the same token must not spin forever.
	counter := pagedGWS(t,
		`{"threads":[{"id":"t1"}],"nextPageToken":"same"}`,
		`{"threads":[{"id":"t2"}],"nextPageToken":"same"}`,
	)

	threads, err := New().ListThreads(t.Context(), "q")
	if err != nil {
		t.Fatalf("ListThreads unexpected error: %v", err)
	}
	if len(threads) != 2 {
		t.Fatalf("ListThreads returned %d threads, want 2", len(threads))
	}
	if got := callCount(t, counter); got != 2 {
		t.Fatalf("gws was called %d times, want it to stop once the token repeats", got)
	}
}

func TestListThreadsReportsRunawayPagination(t *testing.T) {
	// Every reply advertises a fresh page, so the page limit is what has to
	// stop it.
	fakeGWS(t, `echo "{\"threads\":[{\"id\":\"t\"}],\"nextPageToken\":\"$(date +%s%N)\"}"`)

	_, err := New().ListThreads(t.Context(), "q")
	if !errors.Is(err, errTooManyPages) {
		t.Fatalf("ListThreads error = %v, want it to report runaway pagination", err)
	}
}

func TestListThreadsPropagatesCommandFailure(t *testing.T) {
	fakeGWS(t, `echo "not authenticated" >&2
exit 3`)

	_, err := New().ListThreads(t.Context(), "q")
	if err == nil {
		t.Fatal("ListThreads should fail when gws exits non-zero")
	}
	if !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("error %q should surface the stderr emitted by gws", err)
	}
}

func TestListThreadsRejectsInvalidJSON(t *testing.T) {
	fakeGWS(t, `echo "not json"`)

	if _, err := New().ListThreads(t.Context(), "q"); err == nil {
		t.Fatal("ListThreads should fail on malformed JSON")
	}
}

func TestListThreadsHonoursContextCancellation(t *testing.T) {
	fakeGWS(t, `sleep 30`)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	if _, err := New().ListThreads(ctx, "q"); err == nil {
		t.Fatal("ListThreads should fail when the context deadline passes")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("ListThreads ignored context cancellation (took %s)", elapsed)
	}
}

func TestGetThread(t *testing.T) {
	argsFile := echoArgs(t, `{"id":"t1","historyId":"200","messages":[{"id":"m1"}]}`)

	detail, err := New().GetThread(t.Context(), "t1")
	if err != nil {
		t.Fatalf("GetThread unexpected error: %v", err)
	}
	if detail.ID != "t1" || len(detail.Messages) != 1 {
		t.Fatalf("GetThread = %+v", detail)
	}

	recorded, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(recorded), `"id":"t1"`) {
		t.Fatalf("invocation %q missing thread id parameter", recorded)
	}
}

func TestGetThreadPropagatesCommandFailure(t *testing.T) {
	fakeGWS(t, `exit 1`)

	if _, err := New().GetThread(t.Context(), "t1"); err == nil {
		t.Fatal("GetThread should fail when gws exits non-zero")
	}
}

func TestGetThreadRejectsInvalidJSON(t *testing.T) {
	fakeGWS(t, `echo "{"`)

	if _, err := New().GetThread(t.Context(), "t1"); err == nil {
		t.Fatal("GetThread should fail on malformed JSON")
	}
}

func TestListCalendarEvents(t *testing.T) {
	argsFile := echoArgs(t, `{"nextSyncToken":"tok","items":[{"id":"e1","summary":"Standup"}]}`)

	resp, err := New().ListCalendarEvents(t.Context(), CalendarListParams{})
	if err != nil {
		t.Fatalf("ListCalendarEvents unexpected error: %v", err)
	}
	if resp.NextSyncToken != "tok" || len(resp.Items) != 1 {
		t.Fatalf("ListCalendarEvents = %+v", resp)
	}

	recorded, _ := os.ReadFile(argsFile)
	args := string(recorded)
	if !strings.Contains(args, `"calendarId":"primary"`) {
		t.Fatalf("invocation %q should default calendarId to primary", args)
	}
	if !strings.Contains(args, `"singleEvents":true`) {
		t.Fatalf("invocation %q should request single events", args)
	}
}

func TestListCalendarEventsFollowsPaginationAndTakesTheFinalSyncToken(t *testing.T) {
	// Google issues nextSyncToken only on the last page, so stopping early
	// would both truncate the events and lose the incremental sync token.
	counter := pagedGWS(t,
		`{"items":[{"id":"e1"}],"nextPageToken":"p2"}`,
		`{"items":[{"id":"e2"}],"nextPageToken":"p3"}`,
		`{"items":[{"id":"e3"}],"nextSyncToken":"final-token"}`,
	)

	resp, err := New().ListCalendarEvents(t.Context(), CalendarListParams{})
	if err != nil {
		t.Fatalf("ListCalendarEvents unexpected error: %v", err)
	}
	if len(resp.Items) != 3 {
		t.Fatalf("returned %d events, want all 3 across three pages", len(resp.Items))
	}
	if resp.NextSyncToken != "final-token" {
		t.Fatalf("NextSyncToken = %q, want the token from the final page", resp.NextSyncToken)
	}
	if got := callCount(t, counter); got != 3 {
		t.Fatalf("gws was called %d times, want one call per page", got)
	}
}

func TestListCalendarEventsReportsRunawayPagination(t *testing.T) {
	fakeGWS(t, `echo "{\"items\":[],\"nextPageToken\":\"$(date +%s%N)\"}"`)

	_, err := New().ListCalendarEvents(t.Context(), CalendarListParams{})
	if !errors.Is(err, errTooManyPages) {
		t.Fatalf("ListCalendarEvents error = %v, want it to report runaway pagination", err)
	}
}

func TestListCalendarEventsPropagatesCommandFailure(t *testing.T) {
	fakeGWS(t, `exit 1`)

	if _, err := New().ListCalendarEvents(t.Context(), CalendarListParams{CalendarID: "primary"}); err == nil {
		t.Fatal("ListCalendarEvents should fail when gws exits non-zero")
	}
}

func TestListCalendarEventsRejectsInvalidJSON(t *testing.T) {
	fakeGWS(t, `echo "["`)

	if _, err := New().ListCalendarEvents(t.Context(), CalendarListParams{}); err == nil {
		t.Fatal("ListCalendarEvents should fail on malformed JSON")
	}
}

func TestGetHeader(t *testing.T) {
	m := MessageDetail{Payload: Payload{Headers: []Header{
		{Name: "subject", Value: "Weekly digest"},
		{Name: "From", Value: "dana@example.com"},
	}}}

	tests := []struct {
		name   string
		lookup string
		want   string
	}{
		{name: "exact match", lookup: "From", want: "dana@example.com"},
		{name: "case insensitive match", lookup: "Subject", want: "Weekly digest"},
		{name: "missing header", lookup: "Cc", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := m.GetHeader(tt.lookup); got != tt.want {
				t.Fatalf("GetHeader(%q) = %q, want %q", tt.lookup, got, tt.want)
			}
		})
	}
}

func TestHeaderMap(t *testing.T) {
	m := MessageDetail{Payload: Payload{Headers: []Header{
		{Name: "list-unsubscribe", Value: "<https://example.com>"},
		{Name: "AUTO-SUBMITTED", Value: "auto-generated"},
	}}}

	got := m.HeaderMap()
	if got["List-Unsubscribe"] != "<https://example.com>" {
		t.Fatalf("HeaderMap did not canonicalize List-Unsubscribe: %+v", got)
	}
	if got["Auto-Submitted"] != "auto-generated" {
		t.Fatalf("HeaderMap did not canonicalize Auto-Submitted: %+v", got)
	}
}

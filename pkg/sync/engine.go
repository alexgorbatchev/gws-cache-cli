package sync

import (
	"fmt"
	"strconv"
	"time"

	"gws-cache/pkg/classifier"
	"gws-cache/pkg/gmail"
	"gws-cache/pkg/store"
)

type Engine struct {
	store    *store.DB
	client   gmail.Client
	Progress func(format string, args ...any)
}

func NewEngine(s *store.DB, c gmail.Client) *Engine {
	return &Engine{
		store:  s,
		client: c,
		Progress: func(format string, args ...any) {
			// default no-op
		},
	}
}

type SyncOptions struct {
	ForceFull  bool
	Since      string // "4w", "30d", "2026-06-01", "all"
	MaxThreads int    // cap per sync
}

type SyncResult struct {
	TopicSlug        string `json:"topic_slug"`
	ThreadsFetched   int    `json:"threads_fetched"`
	MessagesIngested int    `json:"messages_ingested"`
	MaxHistoryID     string `json:"max_history_id"`
	Error            string `json:"error,omitempty"`
}

func (e *Engine) SyncTopic(slug string, opts SyncOptions) (*SyncResult, error) {
	top, err := e.store.GetTopicBySlug(slug)
	if err != nil {
		top, err = e.store.CreateTopic(slug, slug, "")
		if err != nil {
			return nil, fmt.Errorf("auto-registering topic %q: %w", slug, err)
		}
	}

	runID, err := e.store.StartSyncRun(top.ID)
	if err != nil {
		return nil, fmt.Errorf("starting sync run: %w", err)
	}

	res, syncErr := e.doSyncTopic(top, opts)
	if syncErr != nil {
		threadsFetched, msgsIngested := 0, 0
		maxHistID := ""
		if res != nil {
			threadsFetched = res.ThreadsFetched
			msgsIngested = res.MessagesIngested
			maxHistID = res.MaxHistoryID
		}
		_ = e.store.CompleteSyncRun(runID, "failed", threadsFetched, msgsIngested, maxHistID, syncErr.Error())
		return res, syncErr
	}

	_ = e.store.CompleteSyncRun(runID, "success", res.ThreadsFetched, res.MessagesIngested, res.MaxHistoryID, "")
	return res, nil
}

func (e *Engine) doSyncTopic(top *store.Topic, opts SyncOptions) (*SyncResult, error) {
	queries, err := e.store.ListQueries(top.ID)
	if err != nil || len(queries) == 0 {
		if top.Query != "" {
			queries = []string{top.Query}
		} else {
			queries = []string{top.DisplayName}
		}
	}

	res := &SyncResult{
		TopicSlug: top.Slug,
	}

	maxThreads := opts.MaxThreads
	if maxThreads <= 0 {
		maxThreads = 25
	}

	var cutoffSec int64
	if !opts.ForceFull && top.LastSyncedAt != nil && top.LastMessageInternalDate > 0 {
		// Warm start: 24 hour buffer before last internal date
		cutoffSec = (top.LastMessageInternalDate / 1000) - 86400
	} else if opts.Since != "" {
		cSec, err := ParseSince(opts.Since)
		if err != nil {
			return res, fmt.Errorf("invalid since window %q: %w", opts.Since, err)
		}
		cutoffSec = cSec
	}

	var allThreads []gmail.ThreadSummary
	seenThreadIDs := make(map[string]bool)

	for _, q := range queries {
		queryStr := q
		if cutoffSec > 0 {
			afterStr := time.Unix(cutoffSec, 0).UTC().Format("2006-01-02")
			queryStr = fmt.Sprintf("%s after:%s", q, afterStr)
		}

		e.Progress("Querying Gmail for topic %q: %q...", top.Slug, queryStr)
		threads, err := e.client.ListThreads(queryStr)
		if err != nil {
			return res, fmt.Errorf("listing threads for query %q: %w", queryStr, err)
		}

		for _, t := range threads {
			if !seenThreadIDs[t.ID] {
				seenThreadIDs[t.ID] = true
				allThreads = append(allThreads, t)
			}
		}
	}

	if len(allThreads) > maxThreads {
		allThreads = allThreads[:maxThreads]
		e.Progress("Capped to %d threads for sync", maxThreads)
	}

	res.ThreadsFetched = len(allThreads)
	e.Progress("Fetched %d unique thread(s) for topic %q", len(allThreads), top.Slug)

	var maxHistoryID string
	var maxInternalDate int64

	for i, tSummary := range allThreads {
		// Pre-fetch check against SQLite cache: skip if thread exists and historyId matches
		hasMatch, err := e.store.HasThreadWithHistory(tSummary.ID, tSummary.HistoryID)
		if err == nil && hasMatch {
			e.Progress("[%d/%d] Thread %s cached with history %s (skipped)", i+1, len(allThreads), tSummary.ID, tSummary.HistoryID)
			continue
		}

		threadDetail, err := e.client.GetThread(tSummary.ID)
		if err != nil {
			e.Progress("Warning: skipping thread %s: %v", tSummary.ID, err)
			continue
		}

		hID := threadDetail.HistoryID
		if hID == "" {
			hID = tSummary.HistoryID
		}

		if hID > maxHistoryID {
			maxHistoryID = hID
		}

		if err := e.store.UpsertThread(threadDetail.ID, top.ID, hID, tSummary.Snippet); err != nil {
			return res, fmt.Errorf("upserting thread %s: %w", threadDetail.ID, err)
		}

		for _, m := range threadDetail.Messages {
			internalDate, _ := strconv.ParseInt(m.InternalDate, 10, 64)
			if internalDate > maxInternalDate {
				maxInternalDate = internalDate
			}

			dateStr := m.GetHeader("Date")
			if dateStr == "" {
				dateStr = time.Now().Format(time.RFC3339)
			}
			dateISO := parseISO(dateStr, internalDate)

			from := m.GetHeader("From")
			fromAddr, fromName := parseAddress(from)
			toAddr := m.GetHeader("To")
			subject := m.GetHeader("Subject")

			headersMap := m.HeaderMap()
			cls := classifier.Classify(from, subject, m.Snippet, headersMap)

			msgRecord := &store.Message{
				ID:                   m.ID,
				ThreadID:             threadDetail.ID,
				TopicID:              top.ID,
				InternalDate:         internalDate,
				DateISO:              dateISO,
				FromAddress:          fromAddr,
				FromName:             fromName,
				ToAddress:            toAddr,
				Subject:              subject,
				Snippet:              m.Snippet,
				BodyPlain:            m.Snippet, // store snippet as plain body for now
				IsAutomated:          cls.IsAutomated,
				ClassificationReason: cls.Reason,
			}

			if err := e.store.UpsertMessage(msgRecord); err != nil {
				return res, fmt.Errorf("upserting message %s: %w", m.ID, err)
			}
			res.MessagesIngested++
		}

		e.Progress("[%d/%d] Ingested thread %s (%d msgs)", i+1, len(allThreads), tSummary.ID, len(threadDetail.Messages))
	}

	res.MaxHistoryID = maxHistoryID
	_ = e.store.UpdateTopicSyncState(top.ID, maxHistoryID, maxInternalDate)

	return res, nil
}

func (e *Engine) SyncAllTopics(opts SyncOptions) ([]SyncResult, error) {
	topics, err := e.store.ListTopics()
	if err != nil {
		return nil, fmt.Errorf("listing topics: %w", err)
	}

	var results []SyncResult
	for _, top := range topics {
		res, err := e.SyncTopic(top.Slug, opts)
		if err != nil {
			errStr := err.Error()
			topicSlug := top.Slug
			if res != nil && res.TopicSlug != "" {
				topicSlug = res.TopicSlug
			}
			results = append(results, SyncResult{
				TopicSlug: topicSlug,
				Error:     errStr,
			})
		} else if res != nil {
			results = append(results, *res)
		}
	}

	return results, nil
}

// ParseSince converts lookback duration string into Unix epoch seconds.
func ParseSince(since string) (int64, error) {
	if since == "" || since == "all" {
		return 0, nil
	}

	now := time.Now()

	// Try absolute ISO date format YYYY-MM-DD
	if t, err := time.Parse("2006-01-02", since); err == nil {
		return t.UTC().Unix(), nil
	}

	// Parse relative duration like 4w, 30d, 2m, 24h
	unit := since[len(since)-1]
	valStr := since[:len(since)-1]
	val, err := strconv.Atoi(valStr)
	if err != nil {
		return 0, fmt.Errorf("invalid duration value %q: %w", valStr, err)
	}

	var d time.Duration
	switch unit {
	case 'h':
		d = time.Duration(val) * time.Hour
	case 'd':
		d = time.Duration(val) * 24 * time.Hour
	case 'w':
		d = time.Duration(val) * 7 * 24 * time.Hour
	case 'm':
		d = time.Duration(val) * 30 * 24 * time.Hour
	case 'y':
		d = time.Duration(val) * 365 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("unknown duration unit %q in %q", string(unit), since)
	}

	return now.Add(-d).UTC().Unix(), nil
}

func parseISO(dateStr string, internalDate int64) string {
	if internalDate > 0 {
		return time.UnixMilli(internalDate).UTC().Format(time.RFC3339)
	}
	t, err := time.Parse(time.RFC1123Z, dateStr)
	if err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return time.Now().UTC().Format(time.RFC3339)
}

func parseAddress(from string) (string, string) {
	if from == "" {
		return "", ""
	}
	if idx := len(from) - 1; from[idx] == '>' {
		if start := len(from) - 2; start >= 0 {
			for start >= 0 && from[start] != '<' {
				start--
			}
			if start >= 0 {
				addr := from[start+1 : len(from)-1]
				name := from[:start]
				name = trimQuotes(name)
				return addr, name
			}
		}
	}
	return from, ""
}

func trimQuotes(s string) string {
	s = trimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

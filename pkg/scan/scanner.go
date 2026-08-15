package scan

import (
	"fmt"
	"strconv"
	"time"

	"gws-cache/pkg/classifier"
	"gws-cache/pkg/gmail"
	"gws-cache/pkg/store"
	"gws-cache/pkg/sync"
)

type Scanner struct {
	store    *store.DB
	client   gmail.Client
	Progress func(format string, args ...any)
}

func NewScanner(s *store.DB, c gmail.Client) *Scanner {
	return &Scanner{
		store:  s,
		client: c,
		Progress: func(format string, args ...any) {
			// default no-op
		},
	}
}

type ScanOptions struct {
	Query      string
	Since      string
	MaxThreads int
}

type ScanResult struct {
	ThreadsEvaluated int `json:"threads_evaluated"`
	ThreadsSkipped   int `json:"threads_skipped"`
	MessagesIngested int `json:"messages_ingested"`
}

type SearchOptions struct {
	Keyword   string
	TopicSlug string
	HumanOnly bool
	Since     string
}

func (s *Scanner) ScanInbox(opts ScanOptions) (*ScanResult, error) {
	query := opts.Query
	if query == "" {
		query = "in:inbox"
	}

	cSec, err := sync.ParseSince(opts.Since)
	if err != nil {
		return nil, fmt.Errorf("invalid since window: %w", err)
	}

	if cSec > 0 {
		cutoffStr := time.Unix(cSec, 0).UTC().Format("2006-01-02")
		query = fmt.Sprintf("%s after:%s", query, cutoffStr)
		s.Progress("Scanning inbox for %q (after %s)...", opts.Query, cutoffStr)
	} else {
		s.Progress("Scanning inbox for %q...", query)
	}

	threads, err := s.client.ListThreads(query)
	if err != nil {
		return nil, fmt.Errorf("listing inbox threads: %w", err)
	}

	maxThreads := opts.MaxThreads
	if maxThreads <= 0 {
		maxThreads = 50
	}

	if len(threads) > maxThreads {
		threads = threads[:maxThreads]
		s.Progress("Capped scan to %d threads", maxThreads)
	}

	s.Progress("Evaluating %d thread(s) from Gmail list...", len(threads))

	res := &ScanResult{
		ThreadsEvaluated: len(threads),
	}

	for i, tSummary := range threads {
		hasMatch, err := s.store.HasThreadWithHistory(tSummary.ID, tSummary.HistoryID)
		if err == nil && hasMatch {
			res.ThreadsSkipped++
			shortID := tSummary.ID
			if len(shortID) > 6 {
				shortID = shortID[:4] + "..."
			}
			s.Progress("[%d/%d] Thread %s cached (skipped)", i+1, len(threads), shortID)
			continue
		}

		shortID := tSummary.ID
		if len(shortID) > 6 {
			shortID = shortID[:4] + "..."
		}

		threadDetail, err := s.client.GetThread(tSummary.ID)
		if err != nil {
			s.Progress("Warning: skipping thread %s: %v", shortID, err)
			continue
		}

		hID := threadDetail.HistoryID
		if hID == "" {
			hID = tSummary.HistoryID
		}

		// Store thread under inbox-scan (topic ID 0)
		if err := s.store.UpsertThread(threadDetail.ID, 0, hID, tSummary.Snippet); err != nil {
			return res, fmt.Errorf("upserting thread: %w", err)
		}

		for _, m := range threadDetail.Messages {
			internalDate, _ := strconv.ParseInt(m.InternalDate, 10, 64)
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
				TopicID:              0, // inbox-scan system topic
				InternalDate:         internalDate,
				DateISO:              dateISO,
				FromAddress:          fromAddr,
				FromName:             fromName,
				ToAddress:            toAddr,
				Subject:              subject,
				Snippet:              m.Snippet,
				BodyPlain:            m.Snippet,
				IsAutomated:          cls.IsAutomated,
				ClassificationReason: cls.Reason,
			}

			if err := s.store.UpsertMessage(msgRecord); err != nil {
				return res, fmt.Errorf("upserting message: %w", err)
			}
			res.MessagesIngested++
		}

		s.Progress("[%d/%d] Ingested thread %s (%d msgs)", i+1, len(threads), shortID, len(threadDetail.Messages))
	}

	s.Progress("Scan complete: %d evaluated, %d skipped, %d msgs ingested", res.ThreadsEvaluated, res.ThreadsSkipped, res.MessagesIngested)
	return res, nil
}

func (s *Scanner) SearchCached(opts SearchOptions) ([]store.Message, error) {
	cSec, err := sync.ParseSince(opts.Since)
	if err != nil {
		return nil, fmt.Errorf("invalid since lookback: %w", err)
	}

	return s.store.SearchMessages(opts.Keyword, opts.TopicSlug, opts.HumanOnly, cSec)
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

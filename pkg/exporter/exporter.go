package exporter

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gws-cache/pkg/store"
	"gws-cache/pkg/sync"
)

type Exporter struct {
	store *store.DB
}

func NewExporter(s *store.DB) *Exporter {
	return &Exporter{store: s}
}

type Options struct {
	HumanOnly bool
	Format    string // "json" or "markdown"
	Since     string // "4w", "30d", "2026-06-01", "all"
}

type FormattedMessage struct {
	ID          string `json:"id"`
	ThreadID    string `json:"thread_id"`
	DateISO     string `json:"date_iso"`
	DateDisplay string `json:"date_display"`
	From        string `json:"from"`
	Subject     string `json:"subject"`
	Snippet     string `json:"snippet"`
	IsAutomated bool   `json:"is_automated"`
}

func (e *Exporter) ExportTopic(slug string, opts Options) (string, error) {
	top, err := e.store.GetTopicBySlug(slug)
	if err != nil {
		return "", fmt.Errorf("getting topic %q: %w", slug, err)
	}

	sinceSec, _ := sync.ParseSince(opts.Since)
	msgs, err := e.store.ListMessagesByTopic(top.ID, opts.HumanOnly, sinceSec)
	if err != nil {
		return "", fmt.Errorf("listing messages: %w", err)
	}

	if strings.ToLower(opts.Format) == "json" {
		return e.exportJSON(msgs)
	}

	return e.exportMarkdown(top.DisplayName, msgs)
}

func (e *Exporter) exportJSON(msgs []store.Message) (string, error) {
	var formatted []FormattedMessage
	for _, m := range msgs {
		d, err := time.Parse(time.RFC3339, m.DateISO)
		dateDisp := m.DateISO
		if err == nil {
			dateDisp = d.Format("Jan 02, 2006")
		}

		fromDisp := m.FromName
		if fromDisp == "" {
			fromDisp = m.FromAddress
		}

		formatted = append(formatted, FormattedMessage{
			ID:          m.ID,
			ThreadID:    m.ThreadID,
			DateISO:     m.DateISO,
			DateDisplay: dateDisp,
			From:        fromDisp,
			Subject:     m.Subject,
			Snippet:     m.Snippet,
			IsAutomated: m.IsAutomated,
		})
	}

	b, err := json.MarshalIndent(formatted, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling json: %w", err)
	}
	return string(b), nil
}

func (e *Exporter) exportMarkdown(topicName string, msgs []store.Message) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### %s Timeline\n\n", topicName))

	if len(msgs) == 0 {
		sb.WriteString("*(No messages found)*\n")
		return sb.String(), nil
	}

	for _, m := range msgs {
		d, err := time.Parse(time.RFC3339, m.DateISO)
		dateStr := m.DateISO
		if err == nil {
			dateStr = d.Format("Jan 02, 2006")
		}

		from := m.FromName
		if from == "" {
			from = m.FromAddress
		}

		cleanSnippet := cleanText(m.Snippet)

		sb.WriteString(fmt.Sprintf("* **%s** — %s: %s\n", dateStr, from, cleanSnippet))
	}

	return sb.String(), nil
}

func cleanText(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "&#39;", "'")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&amp;", "&")
	return strings.TrimSpace(s)
}

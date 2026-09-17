// Package exporter renders a topic's cached messages as a timeline.
package exporter

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	"github.com/alexgorbatchev/gws-cache-cli/internal/store"
	"github.com/alexgorbatchev/gws-cache-cli/internal/timewindow"
)

type Exporter struct {
	store *store.DB
}

func NewExporter(s *store.DB) *Exporter {
	return &Exporter{store: s}
}

// Format selects the export rendering.
type Format string

const (
	// FormatMarkdown renders a human-readable timeline.
	FormatMarkdown Format = "markdown"
	// FormatJSON renders a structured timeline.
	FormatJSON Format = "json"
)

// Options controls an export.
type Options struct {
	HumanOnly bool
	Format    Format
	Since     timewindow.Window
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

	// An unbounded window yields zero, which the store reads as no date filter.
	sinceSec, _ := opts.Since.StartUnix(time.Now())
	msgs, err := e.store.ListMessagesByTopic(top.ID, opts.HumanOnly, sinceSec)
	if err != nil {
		return "", fmt.Errorf("listing messages: %w", err)
	}

	if Format(strings.ToLower(string(opts.Format))) == FormatJSON {
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

// cleanText flattens a snippet onto one line. Gmail snippets arrive HTML
// escaped, so they are decoded in full rather than against a hand-written list
// of the few entities that happened to come up.
func cleanText(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(html.UnescapeString(s))
}

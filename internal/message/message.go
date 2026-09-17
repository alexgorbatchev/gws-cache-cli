// Package message converts Gmail payloads into cache rows and ingests whole
// threads. Both the topic sync engine and the inbox scanner go through it so
// that a message is parsed, classified and stored exactly one way.
package message

import (
	"fmt"
	"mime"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/alexgorbatchev/gws-cache-cli/internal/classifier"
	"github.com/alexgorbatchev/gws-cache-cli/internal/gws"
	"github.com/alexgorbatchev/gws-cache-cli/internal/store"
)

// headerDate, headerFrom, headerTo and headerSubject are the RFC 5322 fields
// this cache reads off every message.
const (
	headerDate    = "Date"
	headerFrom    = "From"
	headerTo      = "To"
	headerSubject = "Subject"
)

// Converter turns gws payloads into store rows.
type Converter struct {
	classifier *classifier.Classifier
}

// NewConverter returns a Converter that classifies with the given rules.
func NewConverter(c *classifier.Classifier) Converter {
	return Converter{classifier: c}
}

// FromDetail builds the cache row for a single message. A nil topicID means
// the message was collected by an ad-hoc query and belongs to no saved search.
// now supplies the fallback timestamp used when a message carries no usable
// date.
func (c Converter) FromDetail(detail gws.MessageDetail, threadID string, topicID *int64, now time.Time) (*store.Message, error) {
	internalDate, err := parseInternalDate(detail.InternalDate)
	if err != nil {
		return nil, fmt.Errorf("message %s: %w", detail.ID, err)
	}

	from := detail.GetHeader(headerFrom)
	fromAddress, fromName := ParseAddress(from)
	headers := detail.HeaderMap()
	result := c.classifier.Classify(from, detail.Snippet, headers)

	return &store.Message{
		ID:           detail.ID,
		ThreadID:     threadID,
		TopicID:      topicID,
		InternalDate: internalDate,
		DateISO:      ISODate(detail.GetHeader(headerDate), internalDate, now),
		FromAddress:  fromAddress,
		FromName:     fromName,
		ToAddress:    detail.GetHeader(headerTo),
		Subject:      detail.GetHeader(headerSubject),
		Snippet:      detail.Snippet,
		// Gmail thread payloads expose a snippet rather than a decoded body,
		// so the snippet is what the cache can offer as plain text today.
		BodyPlain:            detail.Snippet,
		IsAutomated:          result.IsAutomated,
		ClassificationReason: result.Reason,
	}, nil
}

// parseInternalDate reads Gmail's epoch-milliseconds field. An absent value is
// acceptable; a malformed one is reported rather than silently treated as the
// epoch, which would corrupt the incremental sync high-watermark.
func parseInternalDate(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid internalDate %q: %w", raw, err)
	}
	return value, nil
}

// ParseAddress splits a From header into an address and a display name,
// falling back to the raw header when it does not parse as RFC 5322.
func ParseAddress(header string) (address, name string) {
	trimmed := strings.TrimSpace(header)
	if trimmed == "" {
		return "", ""
	}

	parser := mail.AddressParser{WordDecoder: &mime.WordDecoder{}}
	parsed, err := parser.Parse(trimmed)
	if err != nil {
		return trimmed, ""
	}
	return parsed.Address, parsed.Name
}

// ISODate renders a message timestamp in RFC 3339. Gmail's internalDate is
// authoritative when present; the Date header is the fallback, and the caller's
// clock is the last resort.
func ISODate(dateHeader string, internalDate int64, now time.Time) string {
	if internalDate > 0 {
		return time.UnixMilli(internalDate).UTC().Format(time.RFC3339)
	}
	if parsed, err := mail.ParseDate(dateHeader); err == nil {
		return parsed.UTC().Format(time.RFC3339)
	}
	return now.UTC().Format(time.RFC3339)
}

// ThreadStats reports what a single thread contributed to the cache.
type ThreadStats struct {
	MessagesIngested int
	MaxInternalDate  int64
}

// Ingestor stores threads and their messages.
type Ingestor struct {
	store     *store.DB
	converter Converter
}

// NewIngestor returns an Ingestor writing to db and classifying with c.
func NewIngestor(db *store.DB, c *classifier.Classifier) *Ingestor {
	return &Ingestor{store: db, converter: NewConverter(c)}
}

// IngestThread upserts a thread and every message it contains.
func (i *Ingestor) IngestThread(detail *gws.ThreadDetail, topicID *int64, historyID, snippet string, now time.Time) (ThreadStats, error) {
	var stats ThreadStats

	if err := i.store.UpsertThread(detail.ID, topicID, historyID, snippet); err != nil {
		return stats, fmt.Errorf("upserting thread %s: %w", detail.ID, err)
	}

	for _, detailMessage := range detail.Messages {
		record, err := i.converter.FromDetail(detailMessage, detail.ID, topicID, now)
		if err != nil {
			return stats, err
		}
		if err := i.store.UpsertMessage(record); err != nil {
			return stats, fmt.Errorf("upserting message %s: %w", record.ID, err)
		}
		if record.InternalDate > stats.MaxInternalDate {
			stats.MaxInternalDate = record.InternalDate
		}
		stats.MessagesIngested++
	}

	return stats, nil
}

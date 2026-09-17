package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrTopicNotFound is returned when a slug does not name a tracked topic.
// Callers branch on it to tell "this topic does not exist yet" apart from a
// genuine database failure, which must never be mistaken for the former.
var ErrTopicNotFound = errors.New("topic not found")

// millisPerSecond converts the epoch seconds used by window filters into the
// epoch milliseconds Gmail stores in internal_date.
const millisPerSecond = 1000

// DefaultDBPath returns the default database file path following XDG Base Directory specification.
func DefaultDBPath() string {
	if xdgCache := os.Getenv("XDG_CACHE_HOME"); xdgCache != "" {
		return filepath.Join(xdgCache, "gws-cache", "cache.db")
	}

	dir, err := os.UserCacheDir()
	if err == nil && dir != "" {
		return filepath.Join(dir, "gws-cache", "cache.db")
	}

	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return filepath.Join(home, ".cache", "gws-cache", "cache.db")
	}

	return filepath.Join(".", "cache.db")
}

type Topic struct {
	ID                      int64      `json:"id"`
	Slug                    string     `json:"slug"`
	DisplayName             string     `json:"display_name"`
	Query                   string     `json:"query"`
	LastSyncedAt            *time.Time `json:"last_synced_at"`
	LastHistoryID           string     `json:"last_history_id"`
	LastMessageInternalDate int64      `json:"last_message_internal_date"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

type Message struct {
	ID                   string    `json:"id"`
	ThreadID             string    `json:"thread_id"`
	TopicID              *int64    `json:"topic_id,omitempty"`
	InternalDate         int64     `json:"internal_date"`
	DateISO              string    `json:"date_iso"`
	FromAddress          string    `json:"from_address"`
	FromName             string    `json:"from_name"`
	ToAddress            string    `json:"to_address"`
	Subject              string    `json:"subject"`
	Snippet              string    `json:"snippet"`
	BodyPlain            string    `json:"body_plain"`
	IsAutomated          bool      `json:"is_automated"`
	ClassificationReason string    `json:"classification_reason"`
	CreatedAt            time.Time `json:"created_at"`
}

type CalendarEvent struct {
	ID             string    `json:"id"`
	CalendarID     string    `json:"calendar_id"`
	TopicID        *int64    `json:"topic_id,omitempty"`
	Summary        string    `json:"summary"`
	Description    string    `json:"description"`
	Location       string    `json:"location"`
	OrganizerEmail string    `json:"organizer_email"`
	StartTime      time.Time `json:"start_time"`
	EndTime        time.Time `json:"end_time"`
	Status         string    `json:"status"` // "confirmed", "tentative", "cancelled"
	IsDeleted      bool      `json:"is_deleted"`
	AttendeesJSON  string    `json:"attendees_json"`
	HTMLLink       string    `json:"html_link"`
	ICalUID        string    `json:"i_cal_uid"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type CalendarSyncState struct {
	CalendarID   string    `json:"calendar_id"`
	SyncToken    string    `json:"sync_token"`
	LastSyncedAt time.Time `json:"last_synced_at"`
	WindowStart  time.Time `json:"window_start"`
	WindowEnd    time.Time `json:"window_end"`
}

type DB struct {
	db *sql.DB
}

// Open initializes and returns a DB handle at the specified file path.
func Open(dbPath string) (*DB, error) {
	if dbPath == "" {
		dbPath = DefaultDBPath()
	}

	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database: %w", err)
	}

	conn.SetMaxOpenConns(1)

	s := &DB{db: conn}
	if err := s.init(); err != nil {
		_ = conn.Close() // best-effort cleanup; the schema error below is what matters
		return nil, fmt.Errorf("initializing schema: %w", err)
	}

	return s, nil
}

func (s *DB) Close() error {
	return s.db.Close()
}

func (s *DB) init() error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON;",
		"PRAGMA journal_mode = WAL;",
		"PRAGMA busy_timeout = 5000;",
	}
	for _, p := range pragmas {
		if _, err := s.db.Exec(p); err != nil {
			return fmt.Errorf("executing %s: %w", p, err)
		}
	}

	schema := `
	CREATE TABLE IF NOT EXISTS topics (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		slug TEXT NOT NULL UNIQUE,
		display_name TEXT NOT NULL,
		query TEXT NOT NULL DEFAULT '',
		last_synced_at DATETIME,
		last_history_id TEXT DEFAULT '',
		last_message_internal_date INTEGER DEFAULT 0,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS topic_queries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		topic_id INTEGER NOT NULL REFERENCES topics(id) ON DELETE CASCADE,
		query_string TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(topic_id, query_string)
	);

	CREATE TABLE IF NOT EXISTS threads (
		id TEXT PRIMARY KEY,
		topic_id INTEGER REFERENCES topics(id) ON DELETE SET NULL,
		history_id TEXT NOT NULL,
		snippet TEXT,
		last_synced_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY,
		thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
		topic_id INTEGER REFERENCES topics(id) ON DELETE SET NULL,
		internal_date INTEGER NOT NULL,
		date_iso TEXT NOT NULL,
		from_address TEXT NOT NULL,
		from_name TEXT,
		to_address TEXT NOT NULL,
		subject TEXT NOT NULL,
		snippet TEXT NOT NULL,
		body_plain TEXT,
		is_automated BOOLEAN NOT NULL DEFAULT 0,
		classification_reason TEXT,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS calendar_sync_state (
		calendar_id TEXT PRIMARY KEY,
		sync_token TEXT NOT NULL,
		last_synced_at DATETIME NOT NULL,
		window_start DATETIME NOT NULL,
		window_end DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS calendar_events (
		id TEXT PRIMARY KEY,
		calendar_id TEXT NOT NULL,
		topic_id INTEGER REFERENCES topics(id) ON DELETE SET NULL,
		summary TEXT NOT NULL,
		description TEXT,
		location TEXT,
		organizer_email TEXT,
		start_time DATETIME NOT NULL,
		end_time DATETIME NOT NULL,
		status TEXT NOT NULL,
		is_deleted BOOLEAN NOT NULL DEFAULT 0,
		attendees_json TEXT,
		html_link TEXT,
		i_cal_uid TEXT,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS sync_runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		topic_id INTEGER REFERENCES topics(id) ON DELETE SET NULL,
		started_at DATETIME NOT NULL,
		completed_at DATETIME,
		status TEXT NOT NULL,
		threads_fetched INTEGER NOT NULL DEFAULT 0,
		messages_ingested INTEGER NOT NULL DEFAULT 0,
		new_max_history_id TEXT,
		error_message TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_messages_topic_date ON messages(topic_id, internal_date ASC);
	CREATE INDEX IF NOT EXISTS idx_messages_topic_automated ON messages(topic_id, is_automated);
	CREATE INDEX IF NOT EXISTS idx_calendar_events_window ON calendar_events(start_time, end_time);
	CREATE INDEX IF NOT EXISTS idx_calendar_events_topic ON calendar_events(topic_id);
	`

	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("creating tables: %w", err)
	}

	return nil
}

func (s *DB) HasThreadWithHistory(threadID, historyID string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM threads WHERE id = ? AND history_id = ?`, threadID, historyID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking thread %s: %w", threadID, err)
	}
	return count > 0, nil
}

func (s *DB) UpsertCalendarEvent(e *CalendarEvent) error {
	var topicIDVal any
	if e.TopicID != nil {
		topicIDVal = *e.TopicID
	}

	_, err := s.db.Exec(`
		INSERT INTO calendar_events (
			id, calendar_id, topic_id, summary, description,
			location, organizer_email, start_time, end_time, status,
			is_deleted, attendees_json, html_link, i_cal_uid, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			topic_id = COALESCE(excluded.topic_id, calendar_events.topic_id),
			summary = excluded.summary,
			description = excluded.description,
			location = excluded.location,
			organizer_email = excluded.organizer_email,
			start_time = excluded.start_time,
			end_time = excluded.end_time,
			status = excluded.status,
			is_deleted = excluded.is_deleted,
			attendees_json = excluded.attendees_json,
			html_link = excluded.html_link,
			i_cal_uid = excluded.i_cal_uid,
			updated_at = CURRENT_TIMESTAMP
	`, e.ID, e.CalendarID, topicIDVal, e.Summary, e.Description,
		e.Location, e.OrganizerEmail, e.StartTime, e.EndTime, e.Status,
		e.IsDeleted, e.AttendeesJSON, e.HTMLLink, e.ICalUID,
	)
	if err != nil {
		return fmt.Errorf("upserting calendar event %s: %w", e.ID, err)
	}
	return nil
}

func (s *DB) GetCalendarSyncState(calendarID string) (*CalendarSyncState, error) {
	row := s.db.QueryRow(`
		SELECT calendar_id, sync_token, last_synced_at, window_start, window_end
		FROM calendar_sync_state
		WHERE calendar_id = ?
	`, calendarID)

	st := &CalendarSyncState{}
	err := row.Scan(&st.CalendarID, &st.SyncToken, &st.LastSyncedAt, &st.WindowStart, &st.WindowEnd)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting calendar sync state for %s: %w", calendarID, err)
	}
	return st, nil
}

func (s *DB) UpdateCalendarSyncState(st *CalendarSyncState) error {
	_, err := s.db.Exec(`
		INSERT INTO calendar_sync_state (calendar_id, sync_token, last_synced_at, window_start, window_end)
		VALUES (?, ?, CURRENT_TIMESTAMP, ?, ?)
		ON CONFLICT(calendar_id) DO UPDATE SET
			sync_token = excluded.sync_token,
			last_synced_at = CURRENT_TIMESTAMP,
			window_start = excluded.window_start,
			window_end = excluded.window_end
	`, st.CalendarID, st.SyncToken, st.WindowStart, st.WindowEnd)
	if err != nil {
		return fmt.Errorf("updating calendar sync state for %s: %w", st.CalendarID, err)
	}
	return nil
}

// SearchCalendarEvents returns cached events matching the given filters. An
// empty keyword matches everything, which is how a plain listing is expressed.
func (s *DB) SearchCalendarEvents(keyword, calendarID, topicSlug string, sinceSec int64) ([]CalendarEvent, error) {
	query := `
		SELECT e.id, e.calendar_id, e.topic_id, e.summary, COALESCE(e.description, ''),
		       COALESCE(e.location, ''), COALESCE(e.organizer_email, ''), e.start_time, e.end_time,
		       e.status, e.is_deleted, COALESCE(e.attendees_json, ''), COALESCE(e.html_link, ''),
		       COALESCE(e.i_cal_uid, ''), e.created_at, e.updated_at
		FROM calendar_events e
	`
	var args []any
	var where []string

	if calendarID != "" {
		where = append(where, "e.calendar_id = ?")
		args = append(args, calendarID)
	}

	if topicSlug != "" {
		where = append(where, "e.topic_id IN (SELECT id FROM topics WHERE slug = ?)")
		args = append(args, topicSlug)
	}

	if keyword != "" {
		like := "%" + keyword + "%"
		where = append(where, "(e.summary LIKE ? OR e.description LIKE ? OR e.location LIKE ? OR e.organizer_email LIKE ?)")
		args = append(args, like, like, like, like)
	}

	if sinceSec > 0 {
		sinceTime := time.Unix(sinceSec, 0).UTC()
		where = append(where, "e.start_time >= ?")
		args = append(args, sinceTime)
	}

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += " ORDER BY e.start_time ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying calendar events: %w", err)
	}
	defer rows.Close()

	var events []CalendarEvent
	for rows.Next() {
		var e CalendarEvent
		var topID sql.NullInt64
		err := rows.Scan(
			&e.ID, &e.CalendarID, &topID, &e.Summary, &e.Description,
			&e.Location, &e.OrganizerEmail, &e.StartTime, &e.EndTime,
			&e.Status, &e.IsDeleted, &e.AttendeesJSON, &e.HTMLLink,
			&e.ICalUID, &e.CreatedAt, &e.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scanning calendar event: %w", err)
		}
		if topID.Valid {
			idVal := topID.Int64
			e.TopicID = &idVal
		}
		events = append(events, e)
	}

	return events, rows.Err()
}

func (s *DB) CreateTopic(slug, displayName, query string) (*Topic, error) {
	if displayName == "" {
		displayName = slug
	}

	res, err := s.db.Exec(`
		INSERT INTO topics (slug, display_name, query)
		VALUES (?, ?, ?)
	`, slug, displayName, query)
	if err != nil {
		return nil, fmt.Errorf("inserting topic %q: %w", slug, err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("getting last insert id: %w", err)
	}

	qStr := query
	if qStr == "" {
		qStr = displayName
	}

	if _, err := s.db.Exec(`
		INSERT OR IGNORE INTO topic_queries (topic_id, query_string)
		VALUES (?, ?)
	`, id, qStr); err != nil {
		return nil, fmt.Errorf("seeding query for topic %q: %w", slug, err)
	}

	return s.GetTopicBySlug(slug)
}

func (s *DB) GetTopicBySlug(slug string) (*Topic, error) {
	row := s.db.QueryRow(`
		SELECT id, slug, display_name, query, last_synced_at, 
		       COALESCE(last_history_id, ''), COALESCE(last_message_internal_date, 0),
		       created_at, updated_at
		FROM topics WHERE slug = ?
	`, slug)

	t := &Topic{}
	var lastSynced sql.NullTime
	err := row.Scan(
		&t.ID, &t.Slug, &t.DisplayName, &t.Query, &lastSynced,
		&t.LastHistoryID, &t.LastMessageInternalDate, &t.CreatedAt, &t.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%q: %w", slug, ErrTopicNotFound)
		}
		return nil, fmt.Errorf("scanning topic %q: %w", slug, err)
	}

	if lastSynced.Valid {
		t.LastSyncedAt = &lastSynced.Time
	}

	return t, nil
}

func (s *DB) ListTopics() ([]Topic, error) {
	rows, err := s.db.Query(`
		SELECT id, slug, display_name, query, last_synced_at, 
		       COALESCE(last_history_id, ''), COALESCE(last_message_internal_date, 0),
		       created_at, updated_at
		FROM topics ORDER BY slug ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("querying topics: %w", err)
	}
	defer rows.Close()

	var result []Topic
	for rows.Next() {
		var t Topic
		var lastSynced sql.NullTime
		if err := rows.Scan(
			&t.ID, &t.Slug, &t.DisplayName, &t.Query, &lastSynced,
			&t.LastHistoryID, &t.LastMessageInternalDate, &t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning topic row: %w", err)
		}
		if lastSynced.Valid {
			t.LastSyncedAt = &lastSynced.Time
		}
		result = append(result, t)
	}

	return result, rows.Err()
}

func (s *DB) DeleteTopic(slug string) error {
	res, err := s.db.Exec(`DELETE FROM topics WHERE slug = ?`, slug)
	if err != nil {
		return fmt.Errorf("deleting topic %q: %w", slug, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("counting rows deleted for topic %q: %w", slug, err)
	}
	if affected == 0 {
		return fmt.Errorf("%q: %w", slug, ErrTopicNotFound)
	}
	return nil
}

func (s *DB) AddQuery(topicID int64, queryString string) error {
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO topic_queries (topic_id, query_string)
		VALUES (?, ?)
	`, topicID, queryString)
	if err != nil {
		return fmt.Errorf("adding query for topic %d: %w", topicID, err)
	}
	return nil
}

func (s *DB) ListQueries(topicID int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT query_string FROM topic_queries WHERE topic_id = ?`, topicID)
	if err != nil {
		return nil, fmt.Errorf("querying topic queries: %w", err)
	}
	defer rows.Close()

	var queries []string
	for rows.Next() {
		var q string
		if err := rows.Scan(&q); err != nil {
			return nil, err
		}
		queries = append(queries, q)
	}
	return queries, rows.Err()
}

// UpsertThread caches a thread. A nil topicID means the thread was collected
// by an ad-hoc query and belongs to no saved search.
func (s *DB) UpsertThread(threadID string, topicID *int64, historyID, snippet string) error {
	_, err := s.db.Exec(`
		INSERT INTO threads (id, topic_id, history_id, snippet, last_synced_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			topic_id = COALESCE(excluded.topic_id, threads.topic_id),
			history_id = excluded.history_id,
			snippet = excluded.snippet,
			last_synced_at = CURRENT_TIMESTAMP
	`, threadID, topicID, historyID, snippet)
	if err != nil {
		return fmt.Errorf("upserting thread %s: %w", threadID, err)
	}
	return nil
}

func (s *DB) UpsertMessage(m *Message) error {
	_, err := s.db.Exec(`
		INSERT INTO messages (
			id, thread_id, topic_id, internal_date, date_iso,
			from_address, from_name, to_address, subject, snippet,
			body_plain, is_automated, classification_reason
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			topic_id = COALESCE(excluded.topic_id, messages.topic_id),
			internal_date = excluded.internal_date,
			date_iso = excluded.date_iso,
			from_address = excluded.from_address,
			from_name = excluded.from_name,
			to_address = excluded.to_address,
			subject = excluded.subject,
			snippet = excluded.snippet,
			body_plain = excluded.body_plain,
			is_automated = excluded.is_automated,
			classification_reason = excluded.classification_reason
	`, m.ID, m.ThreadID, m.TopicID, m.InternalDate, m.DateISO,
		m.FromAddress, m.FromName, m.ToAddress, m.Subject, m.Snippet,
		m.BodyPlain, m.IsAutomated, m.ClassificationReason,
	)
	if err != nil {
		return fmt.Errorf("upserting message %s: %w", m.ID, err)
	}
	return nil
}

// scanMessages reads message rows, tolerating the NULL topic that an ad-hoc
// query sync leaves behind.
func scanMessages(rows *sql.Rows) ([]Message, error) {
	var msgs []Message
	for rows.Next() {
		var m Message
		var topicID sql.NullInt64
		if err := rows.Scan(
			&m.ID, &m.ThreadID, &topicID, &m.InternalDate, &m.DateISO,
			&m.FromAddress, &m.FromName, &m.ToAddress, &m.Subject, &m.Snippet,
			&m.BodyPlain, &m.IsAutomated, &m.ClassificationReason, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning message row: %w", err)
		}
		if topicID.Valid {
			id := topicID.Int64
			m.TopicID = &id
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// MaxMessageInternalDate returns the newest message timestamp cached for a
// topic, or zero when it has none. The incremental watermark is derived from
// this rather than from whatever a single run happened to download, because
// the mark means "everything older than this is already cached" — a property
// of the cache, not of one run.
func (s *DB) MaxMessageInternalDate(topicID int64) (int64, error) {
	var newest sql.NullInt64
	err := s.db.QueryRow(`SELECT MAX(internal_date) FROM messages WHERE topic_id = ?`, topicID).Scan(&newest)
	if err != nil {
		return 0, fmt.Errorf("reading newest cached message for topic %d: %w", topicID, err)
	}
	if !newest.Valid {
		return 0, nil
	}
	return newest.Int64, nil
}

func (s *DB) UpdateTopicSyncState(topicID int64, lastHistoryID string, lastInternalDate int64) error {
	_, err := s.db.Exec(`
		UPDATE topics
		SET last_synced_at = CURRENT_TIMESTAMP,
		    last_history_id = CASE WHEN ? != '' THEN ? ELSE last_history_id END,
		    last_message_internal_date = CASE WHEN ? > last_message_internal_date THEN ? ELSE last_message_internal_date END,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, lastHistoryID, lastHistoryID, lastInternalDate, lastInternalDate, topicID)
	if err != nil {
		return fmt.Errorf("updating topic sync state: %w", err)
	}
	return nil
}

func (s *DB) ListMessagesByTopic(topicID int64, humanOnly bool, sinceSec int64) ([]Message, error) {
	query := `
		SELECT id, thread_id, topic_id, internal_date, date_iso,
		       from_address, COALESCE(from_name, ''), to_address, subject, snippet,
		       COALESCE(body_plain, ''), is_automated, COALESCE(classification_reason, ''), created_at
		FROM messages
		WHERE topic_id = ?
	`
	args := []any{topicID}
	if humanOnly {
		query += " AND is_automated = 0"
	}
	if sinceSec > 0 {
		query += " AND internal_date >= ?"
		args = append(args, sinceSec*millisPerSecond)
	}
	query += " ORDER BY internal_date ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

func (s *DB) SearchMessages(keyword, topicSlug string, humanOnly bool, sinceSec int64) ([]Message, error) {
	query := `
		SELECT m.id, m.thread_id, m.topic_id, m.internal_date, m.date_iso,
		       m.from_address, COALESCE(m.from_name, ''), m.to_address, m.subject, m.snippet,
		       COALESCE(m.body_plain, ''), m.is_automated, COALESCE(m.classification_reason, ''), m.created_at
		FROM messages m
	`
	var where []string
	var args []any

	if topicSlug != "" {
		where = append(where, "m.topic_id IN (SELECT id FROM topics WHERE slug = ?)")
		args = append(args, topicSlug)
	}

	if keyword != "" {
		kw := "%" + keyword + "%"
		where = append(where, "(m.subject LIKE ? OR m.snippet LIKE ? OR m.from_address LIKE ? OR m.from_name LIKE ?)")
		args = append(args, kw, kw, kw, kw)
	}

	if humanOnly {
		where = append(where, "m.is_automated = 0")
	}

	if sinceSec > 0 {
		where = append(where, "m.internal_date >= ?")
		args = append(args, sinceSec*millisPerSecond)
	}

	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}

	query += " ORDER BY m.internal_date DESC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("searching messages: %w", err)
	}
	defer rows.Close()

	return scanMessages(rows)
}

// StartSyncRun opens a sync run record. A nil topicID records an ad-hoc query
// sync that belongs to no saved search.
func (s *DB) StartSyncRun(topicID *int64) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO sync_runs (topic_id, started_at, status)
		VALUES (?, CURRENT_TIMESTAMP, 'running')
	`, topicID)
	if err != nil {
		return 0, fmt.Errorf("starting sync run: %w", err)
	}
	return res.LastInsertId()
}

func (s *DB) CompleteSyncRun(runID int64, status string, threadsFetched, messagesIngested int, maxHistoryID, errMsg string) error {
	_, err := s.db.Exec(`
		UPDATE sync_runs
		SET completed_at = CURRENT_TIMESTAMP,
		    status = ?,
		    threads_fetched = ?,
		    messages_ingested = ?,
		    new_max_history_id = ?,
		    error_message = ?
		WHERE id = ?
	`, status, threadsFetched, messagesIngested, maxHistoryID, errMsg, runID)
	if err != nil {
		return fmt.Errorf("completing sync run %d: %w", runID, err)
	}
	return nil
}

// Stats summarizes what the cache currently holds.
type Stats struct {
	Topics   int `json:"topics"`
	Threads  int `json:"threads"`
	Messages int `json:"messages"`
}

// Stats counts the cached rows, excluding the reserved inbox-scan topic.
func (s *DB) Stats() (Stats, error) {
	var stats Stats
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM topics`).Scan(&stats.Topics); err != nil {
		return Stats{}, fmt.Errorf("counting topics: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM threads`).Scan(&stats.Threads); err != nil {
		return Stats{}, fmt.Errorf("counting threads: %w", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages`).Scan(&stats.Messages); err != nil {
		return Stats{}, fmt.Errorf("counting messages: %w", err)
	}
	return stats, nil
}

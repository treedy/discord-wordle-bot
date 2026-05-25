package main

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	_ "modernc.org/sqlite"
)

const defaultHistoryDBPath = "wordle_history.db"

var historySubmissionScorePattern = regexp.MustCompile(`^\s*(?i:(?:Wordle|Scoredle))\s*[0-9,]*\s*([1-6]|X)/6\*?\s*$`)

type historySubmissionRecord struct {
	ThreadDate  time.Time
	UserID      string
	Guesses     int
	SubmittedAt *time.Time
	Source      string
}

type historyReminderRecord struct {
	ThreadDate time.Time
	RemindedAt *time.Time
}

type periodSubmissionRow struct {
	ThreadDate  string
	UserID      string
	Guesses     int
	SubmittedAt *time.Time
}

type periodReminderRow struct {
	ThreadDate string
	RemindedAt *time.Time
}

type historyStore struct {
	db *sql.DB
}

func openHistoryStore(dbPath string) (*historyStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	store := &historyStore{db: db}
	if err := store.initSchema(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *historyStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *historyStore) initSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("initialize history schema: nil database")
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS submissions (
thread_date TEXT NOT NULL CHECK(thread_date <> ''),
user_id TEXT NOT NULL CHECK(user_id <> ''),
guesses INTEGER NOT NULL CHECK(guesses >= -1),
submitted_at TEXT,
source TEXT NOT NULL CHECK(source <> ''),
PRIMARY KEY (thread_date, user_id)
)`,
		`CREATE TABLE IF NOT EXISTS reminders (
thread_date TEXT NOT NULL CHECK(thread_date <> ''),
reminded_at TEXT,
PRIMARY KEY (thread_date)
)`,
		`CREATE TABLE IF NOT EXISTS users (
user_id TEXT NOT NULL CHECK(user_id <> ''),
display_name TEXT NOT NULL CHECK(display_name <> ''),
PRIMARY KEY (user_id)
)`,
	}

	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize history schema: %w", err)
		}
	}

	return nil
}

type userRow struct {
	UserID      string
	DisplayName string
}

func (s *historyStore) listUsers(ctx context.Context) ([]userRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("list users: nil database")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT user_id, display_name FROM users ORDER BY display_name`)
	if err != nil {
		return nil, fmt.Errorf("list users: query: %w", err)
	}
	defer rows.Close()

	users := make([]userRow, 0)
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.UserID, &u.DisplayName); err != nil {
			return nil, fmt.Errorf("list users: scan: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list users: iterate: %w", err)
	}
	return users, nil
}

func (s *historyStore) addUser(ctx context.Context, userID, displayName string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("add user: nil database")
	}
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(displayName) == "" {
		return fmt.Errorf("add user: user_id and display_name are required")
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO users (user_id, display_name) VALUES (?, ?) ON CONFLICT(user_id) DO UPDATE SET display_name = excluded.display_name`, userID, displayName); err != nil {
		return fmt.Errorf("add user: exec: %w", err)
	}
	return nil
}

func (s *historyStore) writeScanHistory(ctx context.Context, submissions []historySubmissionRecord, reminder historyReminderRecord) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("write scan history: nil database")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("write scan history: begin transaction: %w", err)
	}

	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	for _, submission := range submissions {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO submissions (thread_date, user_id, guesses, submitted_at, source)
 VALUES (?, ?, ?, ?, ?)
 ON CONFLICT(thread_date, user_id) DO UPDATE SET
guesses = excluded.guesses,
submitted_at = excluded.submitted_at,
source = excluded.source`,
			normalizeThreadDate(submission.ThreadDate),
			submission.UserID,
			submission.Guesses,
			normalizeTimestampRFC3339UTC(submission.SubmittedAt),
			submission.Source,
		); err != nil {
			return fmt.Errorf("write scan history: upsert submission %q: %w", submission.UserID, err)
		}
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO reminders (thread_date, reminded_at)
 VALUES (?, ?)
 ON CONFLICT(thread_date) DO UPDATE SET
reminded_at = excluded.reminded_at`,
		normalizeThreadDate(reminder.ThreadDate),
		normalizeTimestampRFC3339UTC(reminder.RemindedAt),
	); err != nil {
		return fmt.Errorf("write scan history: upsert reminder: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("write scan history: commit transaction: %w", err)
	}
	committed = true
	return nil
}

func (s *historyStore) queryPeriodStats(ctx context.Context, startDate, endDate time.Time) ([]periodSubmissionRow, []periodReminderRow, error) {
	if s == nil || s.db == nil {
		return nil, nil, fmt.Errorf("query period stats: nil database")
	}

	submissionRows, err := s.db.QueryContext(
		ctx,
		`SELECT thread_date, user_id, guesses, submitted_at
FROM submissions
WHERE thread_date >= ? AND thread_date < ?
ORDER BY thread_date, user_id`,
		normalizeThreadDate(startDate),
		normalizeThreadDate(endDate),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("query period stats: submissions: %w", err)
	}
	defer submissionRows.Close()

	submissions := make([]periodSubmissionRow, 0)
	for submissionRows.Next() {
		var row periodSubmissionRow
		var submittedAt sql.NullString
		if err := submissionRows.Scan(&row.ThreadDate, &row.UserID, &row.Guesses, &submittedAt); err != nil {
			return nil, nil, fmt.Errorf("query period stats: scan submission row: %w", err)
		}
		if submittedAt.Valid {
			parsed, err := time.Parse(time.RFC3339, submittedAt.String)
			if err != nil {
				return nil, nil, fmt.Errorf("query period stats: parse submission timestamp for %q on %s: %w", row.UserID, row.ThreadDate, err)
			}
			row.SubmittedAt = &parsed
		}
		submissions = append(submissions, row)
	}
	if err := submissionRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("query period stats: iterate submission rows: %w", err)
	}

	reminderRows, err := s.db.QueryContext(
		ctx,
		`SELECT thread_date, reminded_at
FROM reminders
WHERE thread_date >= ? AND thread_date < ?
ORDER BY thread_date`,
		normalizeThreadDate(startDate),
		normalizeThreadDate(endDate),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("query period stats: reminders: %w", err)
	}
	defer reminderRows.Close()

	reminders := make([]periodReminderRow, 0)
	for reminderRows.Next() {
		var row periodReminderRow
		var remindedAt sql.NullString
		if err := reminderRows.Scan(&row.ThreadDate, &remindedAt); err != nil {
			return nil, nil, fmt.Errorf("query period stats: scan reminder row: %w", err)
		}
		if remindedAt.Valid {
			parsed, err := time.Parse(time.RFC3339, remindedAt.String)
			if err != nil {
				return nil, nil, fmt.Errorf("query period stats: parse reminder timestamp for %s: %w", row.ThreadDate, err)
			}
			row.RemindedAt = &parsed
		}
		reminders = append(reminders, row)
	}
	if err := reminderRows.Err(); err != nil {
		return nil, nil, fmt.Errorf("query period stats: iterate reminder rows: %w", err)
	}

	return submissions, reminders, nil
}

func normalizeThreadDate(t time.Time) string {
	return t.UTC().Format(scanDateLayout)
}

func normalizeTimestampRFC3339UTC(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func buildScanHistoryRecords(targetDate time.Time, trackedUserIDs []string, botUserID string, location *time.Location, msgs []*discordgo.Message) ([]historySubmissionRecord, historyReminderRecord) {
	earliestTrackedSubmissions := earliestCanonicalTrackedSubmissions(trackedUserIDs, msgs)
	submissions := make([]historySubmissionRecord, 0, len(trackedUserIDs))
	for _, userID := range trackedUserIDs {
		record := historySubmissionRecord{
			ThreadDate: targetDate,
			UserID:     userID,
			Guesses:    -1,
			Source:     "tracked-missing",
		}
		if submission, ok := earliestTrackedSubmissions[userID]; ok {
			timestamp := submission.SubmittedAt
			record.SubmittedAt = &timestamp
			record.Guesses = submission.Guesses
			record.Source = "tracked-submission"
		}
		submissions = append(submissions, record)
	}

	return submissions, historyReminderRecord{
		ThreadDate: targetDate,
		RemindedAt: earliestValidBotReminderTimestamp(msgs, botUserID, targetDate, location),
	}
}

type canonicalTrackedSubmission struct {
	Guesses     int
	SubmittedAt time.Time
}

func earliestCanonicalTrackedSubmissions(trackedUserIDs []string, msgs []*discordgo.Message) map[string]canonicalTrackedSubmission {
	tracked := make(map[string]struct{}, len(trackedUserIDs))
	for _, userID := range trackedUserIDs {
		tracked[userID] = struct{}{}
	}

	earliest := make(map[string]canonicalTrackedSubmission, len(trackedUserIDs))
	for _, msg := range msgs {
		if !isQualifyingSubmission(msg) {
			continue
		}
		if _, ok := tracked[msg.Author.ID]; !ok {
			continue
		}
		guesses, ok := parseSubmissionGuessesFromFirstLine(msg.Content)
		if !ok {
			continue
		}

		timestamp := msg.Timestamp
		if timestamp.IsZero() {
			continue
		}
		if existing, ok := earliest[msg.Author.ID]; !ok || timestamp.Before(existing.SubmittedAt) {
			earliest[msg.Author.ID] = canonicalTrackedSubmission{
				Guesses:     guesses,
				SubmittedAt: timestamp,
			}
		}
	}

	return earliest
}

func earliestValidBotReminderTimestamp(msgs []*discordgo.Message, botUserID string, targetDate time.Time, location *time.Location) *time.Time {
	if botUserID == "" || location == nil {
		return nil
	}

	var earliest *time.Time
	for _, msg := range msgs {
		if msg == nil || msg.Author == nil {
			continue
		}
		if msg.Author.ID != botUserID {
			continue
		}
		if !isTopLevelMessage(msg) || !reminderPattern.MatchString(msg.Content) || msg.Timestamp.IsZero() {
			continue
		}
		if !sameCalendarDay(msg.Timestamp, targetDate, location) {
			continue
		}

		timestamp := msg.Timestamp
		if earliest == nil || timestamp.Before(*earliest) {
			earliest = &timestamp
		}
	}

	return earliest
}

func parseSubmissionGuessesFromFirstLine(content string) (int, bool) {
	firstLine, _, _ := strings.Cut(content, "\n")
	firstLine = strings.TrimSuffix(firstLine, "\r")

	matches := historySubmissionScorePattern.FindStringSubmatch(firstLine)
	if len(matches) != 2 {
		return 0, false
	}
	switch matches[1] {
	case "X":
		return 7, true
	case "1":
		return 1, true
	case "2":
		return 2, true
	case "3":
		return 3, true
	case "4":
		return 4, true
	case "5":
		return 5, true
	case "6":
		return 6, true
	default:
		return 0, false
	}
}

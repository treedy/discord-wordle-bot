package main

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	_ "modernc.org/sqlite"
)

const defaultHistoryDBPath = "wordle_history.db"

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
	}

	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize history schema: %w", err)
		}
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

func normalizeThreadDate(t time.Time) string {
	return t.UTC().Format(scanDateLayout)
}

func normalizeTimestampRFC3339UTC(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func buildScanHistoryRecords(targetDate time.Time, trackedUserIDs []string, msgs []*discordgo.Message) ([]historySubmissionRecord, historyReminderRecord) {
	earliestTrackedSubmissions := earliestQualifyingTrackedSubmissions(trackedUserIDs, msgs)
	submissions := make([]historySubmissionRecord, 0, len(trackedUserIDs))
	for _, userID := range trackedUserIDs {
		record := historySubmissionRecord{
			ThreadDate: targetDate,
			UserID:     userID,
			Guesses:    -1,
			Source:     "tracked-missing",
		}
		if submittedAt, ok := earliestTrackedSubmissions[userID]; ok {
			timestamp := submittedAt
			record.Guesses = 0
			record.SubmittedAt = &timestamp
			record.Source = "tracked-submission"
		}
		submissions = append(submissions, record)
	}

	return submissions, historyReminderRecord{ThreadDate: targetDate}
}

func earliestQualifyingTrackedSubmissions(trackedUserIDs []string, msgs []*discordgo.Message) map[string]time.Time {
	tracked := make(map[string]struct{}, len(trackedUserIDs))
	for _, userID := range trackedUserIDs {
		tracked[userID] = struct{}{}
	}

	earliest := make(map[string]time.Time, len(trackedUserIDs))
	for _, msg := range msgs {
		if !isQualifyingSubmission(msg) {
			continue
		}
		if _, ok := tracked[msg.Author.ID]; !ok {
			continue
		}

		timestamp := msg.Timestamp
		if timestamp.IsZero() {
			continue
		}
		if existing, ok := earliest[msg.Author.ID]; !ok || timestamp.Before(existing) {
			earliest[msg.Author.ID] = timestamp
		}
	}

	return earliest
}

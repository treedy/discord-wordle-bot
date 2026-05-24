package main

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestParseSubmissionGuessesFromFirstLine(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
		wantOK  bool
	}{
		{
			name:    "accepts strict score from first line",
			content: "Wordle 123 4/6\n⬛⬛⬛⬛⬛",
			want:    4,
			wantOK:  true,
		},
		{
			name:    "maps loss to seven",
			content: "Scoredle 123 X/6*\n⬛⬛⬛⬛⬛",
			want:    7,
			wantOK:  true,
		},
		{
			name:    "rejects missing first line token",
			content: "Wordle 123\n4/6",
			wantOK:  false,
		},
		{
			name:    "rejects malformed token",
			content: "Wordle 123 7/6",
			wantOK:  false,
		},
		{
			name:    "rejects noncanonical text before score",
			content: "Wordle some random text 4/6",
			wantOK:  false,
		},
		{
			name:    "rejects trailing content after token",
			content: "Wordle 123 4/6 extra",
			wantOK:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotOK := parseSubmissionGuessesFromFirstLine(tt.content)
			if gotOK != tt.wantOK {
				t.Fatalf("parseSubmissionGuessesFromFirstLine() ok = %v, want %v", gotOK, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("parseSubmissionGuessesFromFirstLine() guesses = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestEarliestCanonicalTrackedSubmissionsUsesEarliestParseableTrackedTimestamp(t *testing.T) {
	earlier := time.Date(2026, time.April, 18, 8, 15, 0, 0, time.UTC)
	later := earlier.Add(2 * time.Hour)

	got := earliestCanonicalTrackedSubmissions(
		[]string{"234567890123456789"},
		[]*discordgo.Message{
			{Author: &discordgo.User{ID: "234567890123456789"}, Content: "Wordle 123\n4/6", Timestamp: earlier.Add(-time.Hour)},
			{Author: &discordgo.User{ID: "234567890123456789"}, Content: "Wordle 123 4/6", Timestamp: later},
			{Author: &discordgo.User{ID: "234567890123456789"}, Content: "Wordle 123 3/6", Timestamp: earlier},
			{Author: &discordgo.User{ID: "345678901234567890"}, Content: "Wordle 123 2/6", Timestamp: earlier},
			{Author: &discordgo.User{ID: "234567890123456789"}, Content: "Wordle 123 X/6"},
			{Author: &discordgo.User{ID: "234567890123456789"}, Content: "I did Wordle 123 1/6", Timestamp: earlier},
		},
	)

	if len(got) != 1 {
		t.Fatalf("len(earliestCanonicalTrackedSubmissions()) = %d, want %d", len(got), 1)
	}
	if got["234567890123456789"].SubmittedAt != earlier {
		t.Fatalf("earliestCanonicalTrackedSubmissions()[tracked].SubmittedAt = %v, want %v", got["234567890123456789"].SubmittedAt, earlier)
	}
	if got["234567890123456789"].Guesses != 3 {
		t.Fatalf("earliestCanonicalTrackedSubmissions()[tracked].Guesses = %d, want %d", got["234567890123456789"].Guesses, 3)
	}
}

func TestBuildScanHistoryRecordsUsesTrackedSubmissionAndMissRows(t *testing.T) {
	targetDate := time.Date(2026, time.April, 18, 0, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
	submittedAt := time.Date(2026, time.April, 18, 8, 15, 0, 0, time.FixedZone("EDT", -4*60*60))

	submissions, reminder := buildScanHistoryRecords(
		targetDate,
		[]string{"234567890123456789", "345678901234567890"},
		[]*discordgo.Message{
			{Author: &discordgo.User{ID: "234567890123456789"}, Content: "Wordle 123 4/6", Timestamp: submittedAt},
		},
	)

	if len(submissions) != 2 {
		t.Fatalf("len(buildScanHistoryRecords() submissions) = %d, want %d", len(submissions), 2)
	}
	if submissions[0].UserID != "234567890123456789" || submissions[0].Guesses != 4 || submissions[0].SubmittedAt == nil || !submissions[0].SubmittedAt.Equal(submittedAt) || submissions[0].Source != "tracked-submission" {
		t.Fatalf("first submission = %+v, want tracked-submission with parsed guesses and timestamp", submissions[0])
	}
	if submissions[1].UserID != "345678901234567890" || submissions[1].Guesses != -1 || submissions[1].SubmittedAt != nil || submissions[1].Source != "tracked-missing" {
		t.Fatalf("second submission = %+v, want tracked-missing miss row", submissions[1])
	}
	if !reminder.ThreadDate.Equal(targetDate) || reminder.RemindedAt != nil {
		t.Fatalf("reminder = %+v, want target date and nil reminded_at", reminder)
	}
}

func TestHistoryStoreAutoInitializesSchemaAndUpsertsDeterministically(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")

	store, err := openHistoryStore(dbPath)
	if err != nil {
		t.Fatalf("openHistoryStore() error = %v", err)
	}
	defer store.Close()

	var tableCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name IN ('submissions', 'reminders')`).Scan(&tableCount); err != nil {
		t.Fatalf("QueryRow() schema error = %v", err)
	}
	if tableCount != 2 {
		t.Fatalf("schema table count = %d, want %d", tableCount, 2)
	}

	targetDate := time.Date(2026, time.April, 18, 0, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
	firstSubmittedAt := time.Date(2026, time.April, 18, 8, 30, 0, 0, time.FixedZone("EDT", -4*60*60))
	firstReminderAt := time.Date(2026, time.April, 18, 9, 15, 0, 0, time.FixedZone("PDT", -7*60*60))
	if err := store.writeScanHistory(context.Background(), []historySubmissionRecord{{
		ThreadDate:  targetDate,
		UserID:      "234567890123456789",
		Guesses:     4,
		SubmittedAt: &firstSubmittedAt,
		Source:      "tracked-submission",
	}}, historyReminderRecord{ThreadDate: targetDate, RemindedAt: &firstReminderAt}); err != nil {
		t.Fatalf("writeScanHistory() first run error = %v", err)
	}

	secondSubmittedAt := time.Date(2026, time.April, 18, 15, 45, 0, 0, time.FixedZone("PDT", -7*60*60))
	secondReminderAt := time.Date(2026, time.April, 18, 13, 5, 0, 0, time.FixedZone("EDT", -4*60*60))
	if err := store.writeScanHistory(context.Background(), []historySubmissionRecord{{
		ThreadDate:  targetDate,
		UserID:      "234567890123456789",
		Guesses:     5,
		SubmittedAt: &secondSubmittedAt,
		Source:      "tracked-submission",
	}}, historyReminderRecord{ThreadDate: targetDate, RemindedAt: &secondReminderAt}); err != nil {
		t.Fatalf("writeScanHistory() second run error = %v", err)
	}

	var submissionCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM submissions WHERE thread_date = ?`, normalizeThreadDate(targetDate)).Scan(&submissionCount); err != nil {
		t.Fatalf("QueryRow() submission count error = %v", err)
	}
	if submissionCount != 1 {
		t.Fatalf("submission count = %d, want %d", submissionCount, 1)
	}

	var guesses int
	var submittedAt string
	var source string
	if err := store.db.QueryRow(`SELECT guesses, submitted_at, source FROM submissions WHERE thread_date = ? AND user_id = ?`, normalizeThreadDate(targetDate), "234567890123456789").Scan(&guesses, &submittedAt, &source); err != nil {
		t.Fatalf("QueryRow() submission row error = %v", err)
	}
	if guesses != 5 {
		t.Fatalf("stored guesses = %d, want %d", guesses, 5)
	}
	if submittedAt != secondSubmittedAt.UTC().Format(time.RFC3339) {
		t.Fatalf("stored submitted_at = %q, want %q", submittedAt, secondSubmittedAt.UTC().Format(time.RFC3339))
	}
	if source != "tracked-submission" {
		t.Fatalf("stored source = %q, want %q", source, "tracked-submission")
	}

	var remindedAt string
	if err := store.db.QueryRow(`SELECT reminded_at FROM reminders WHERE thread_date = ?`, normalizeThreadDate(targetDate)).Scan(&remindedAt); err != nil {
		t.Fatalf("QueryRow() reminder row error = %v", err)
	}
	if remindedAt != secondReminderAt.UTC().Format(time.RFC3339) {
		t.Fatalf("stored reminded_at = %q, want %q", remindedAt, secondReminderAt.UTC().Format(time.RFC3339))
	}
}

func TestHistoryStoreRollsBackTransactionOnFailure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "history.db")

	store, err := openHistoryStore(dbPath)
	if err != nil {
		t.Fatalf("openHistoryStore() error = %v", err)
	}
	defer store.Close()

	targetDate := time.Date(2026, time.April, 18, 0, 0, 0, 0, time.UTC)
	if err := store.writeScanHistory(context.Background(), []historySubmissionRecord{{
		ThreadDate: targetDate,
		UserID:     "234567890123456789",
		Guesses:    4,
		Source:     "tracked-submission",
	}, {
		ThreadDate: targetDate,
		UserID:     "",
		Guesses:    3,
		Source:     "tracked-submission",
	}}, historyReminderRecord{ThreadDate: targetDate}); err == nil {
		t.Fatal("writeScanHistory() error = nil, want non-nil")
	}

	var submissionCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM submissions`).Scan(&submissionCount); err != nil {
		t.Fatalf("QueryRow() submission count error = %v", err)
	}
	if submissionCount != 0 {
		t.Fatalf("submission count after rollback = %d, want %d", submissionCount, 0)
	}

	var reminderCount int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM reminders`).Scan(&reminderCount); err != nil {
		t.Fatalf("QueryRow() reminder count error = %v", err)
	}
	if reminderCount != 0 {
		t.Fatalf("reminder count after rollback = %d, want %d", reminderCount, 0)
	}
}

func TestRunScanHistoryPersistsTrackedRowsToSQLite(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789", "345678901234567890"],
  "timezone": "America/New_York"
}`)
	dbPath := filepath.Join(t.TempDir(), "history.db")

	originalNewDiscordSession := newDiscordSession
	originalListActiveThreads := listActiveThreadsFn
	originalListArchivedThreads := listArchivedThreadsFn
	originalMessagesInChannel := messagesInChannelFn
	t.Cleanup(func() {
		newDiscordSession = originalNewDiscordSession
		listActiveThreadsFn = originalListActiveThreads
		listArchivedThreadsFn = originalListArchivedThreads
		messagesInChannelFn = originalMessagesInChannel
	})

	expectedThreadID := discordSnowflakeID(time.Date(2026, time.April, 18, 16, 0, 0, 0, time.UTC))
	submittedAt := time.Date(2026, time.April, 18, 8, 15, 0, 0, time.FixedZone("EDT", -4*60*60))

	newDiscordSession = func(botToken string) (*discordgo.Session, error) {
		return &discordgo.Session{}, nil
	}
	listActiveThreadsFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
		return nil, nil
	}
	listArchivedThreadsFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
		return []*discordgo.Channel{{ID: expectedThreadID, Name: "Apr 18"}}, nil
	}
	messagesInChannelFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Message, error) {
		if channelID != expectedThreadID {
			t.Fatalf("messagesInChannelFn() channelID = %q, want %q", channelID, expectedThreadID)
		}
		return []*discordgo.Message{{
			Author:    &discordgo.User{ID: "234567890123456789"},
			Content:   "Wordle 123 4/6",
			Timestamp: submittedAt,
		}}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	args := []string{"discord-wordle-bot", scanHistoryCommand, "--config", configPath, "--date", "2026-04-18", "--db-path", dbPath}
	if exitCode := runCLI(args, &stdout, &stderr, time.Now); exitCode != exitSuccess {
		t.Fatalf("runCLI() first exitCode = %d, want %d (stderr=%q)", exitCode, exitSuccess, stderr.String())
	}
	if exitCode := runCLI(args, &stdout, &stderr, time.Now); exitCode != exitSuccess {
		t.Fatalf("runCLI() second exitCode = %d, want %d (stderr=%q)", exitCode, exitSuccess, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("persisted scan history submissions=2 reminder_timestamp_present=false")) {
		t.Fatalf("stdout = %q, want persistence summary log", stdout.String())
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	targetDate, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}
	threadDate, err := parseScanDate("2026-04-18", targetDate)
	if err != nil {
		t.Fatalf("parseScanDate() error = %v", err)
	}

	rows, err := db.Query(`SELECT user_id, guesses, submitted_at, source FROM submissions WHERE thread_date = ? ORDER BY user_id`, normalizeThreadDate(threadDate))
	if err != nil {
		t.Fatalf("Query() submission rows error = %v", err)
	}
	defer rows.Close()

	type submissionRow struct {
		userID      string
		guesses     int
		submittedAt sql.NullString
		source      string
	}

	var got []submissionRow
	for rows.Next() {
		var row submissionRow
		if err := rows.Scan(&row.userID, &row.guesses, &row.submittedAt, &row.source); err != nil {
			t.Fatalf("rows.Scan() error = %v", err)
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(submission rows) = %d, want %d", len(got), 2)
	}
	if got[0].userID != "234567890123456789" || got[0].guesses != 4 || !got[0].submittedAt.Valid || got[0].submittedAt.String != submittedAt.UTC().Format(time.RFC3339) || got[0].source != "tracked-submission" {
		t.Fatalf("first submission row = %+v, want tracked submission with parsed guesses and UTC timestamp", got[0])
	}
	if got[1].userID != "345678901234567890" || got[1].guesses != -1 || got[1].submittedAt.Valid || got[1].source != "tracked-missing" {
		t.Fatalf("second submission row = %+v, want tracked miss row", got[1])
	}

	var reminderCount int
	var remindedAt sql.NullString
	if err := db.QueryRow(`SELECT COUNT(*), reminded_at FROM reminders WHERE thread_date = ?`, normalizeThreadDate(threadDate)).Scan(&reminderCount, &remindedAt); err != nil {
		t.Fatalf("QueryRow() reminder row error = %v", err)
	}
	if reminderCount != 1 {
		t.Fatalf("reminder row count = %d, want %d", reminderCount, 1)
	}
	if remindedAt.Valid {
		t.Fatalf("reminded_at valid = %v, want false", remindedAt.Valid)
	}
}

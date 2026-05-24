package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestRunCLIRequiresPeriodForReportStats(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789"],
  "timezone": "America/New_York"
}`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runCLI([]string{"discord-wordle-bot", reportStatsCommand, "--config", configPath, "--date", "2026-04-18"}, &stdout, &stderr, time.Now)
	if exitCode != exitConfigError {
		t.Fatalf("runCLI() exitCode = %d, want %d", exitCode, exitConfigError)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "configuration error: --period is required") {
		t.Fatalf("stderr = %q, want missing-period error", stderr.String())
	}
}

func TestRunCLIRequiresValidDateForReportStats(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789"],
  "timezone": "America/New_York"
}`)

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "missing date",
			args:    []string{"discord-wordle-bot", reportStatsCommand, "--config", configPath, "--period", "daily"},
			wantErr: "configuration error: --date is required",
		},
		{
			name:    "malformed date",
			args:    []string{"discord-wordle-bot", reportStatsCommand, "--config", configPath, "--period", "daily", "--date", "2026-04-31"},
			wantErr: `configuration error: invalid --date "2026-04-31": must be a real calendar date in YYYY-MM-DD`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer

			exitCode := runCLI(tt.args, &stdout, &stderr, time.Now)
			if exitCode != exitConfigError {
				t.Fatalf("runCLI() exitCode = %d, want %d", exitCode, exitConfigError)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), tt.wantErr) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tt.wantErr)
			}
		})
	}
}

func TestRunCLIRejectsUnrecognizedPeriodForReportStats(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789"],
  "timezone": "America/New_York"
}`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runCLI([]string{"discord-wordle-bot", reportStatsCommand, "--config", configPath, "--period", "monthly", "--date", "2026-04-18"}, &stdout, &stderr, time.Now)
	if exitCode != exitConfigError {
		t.Fatalf("runCLI() exitCode = %d, want %d", exitCode, exitConfigError)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), `configuration error: invalid --period "monthly": supported values: daily`) {
		t.Fatalf("stderr = %q, want invalid-period error", stderr.String())
	}
}

func TestRunReportStatsDailyOutputsTrackedUsersInConfigOrder(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["345678901234567890", "234567890123456789", "456789012345678901"],
  "timezone": "America/New_York"
}`)
	dbPath := filepath.Join(t.TempDir(), "history.db")

	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}

	targetDate, err := parseScanDate("2026-04-18", location)
	if err != nil {
		t.Fatalf("parseScanDate() error = %v", err)
	}

	store, err := openHistoryStore(dbPath)
	if err != nil {
		t.Fatalf("openHistoryStore() error = %v", err)
	}
	defer store.Close()

	remindedAt := time.Date(2026, time.April, 18, 15, 0, 0, 0, time.UTC)
	solvedAt := time.Date(2026, time.April, 18, 16, 0, 0, 0, time.UTC)
	missedAt := time.Date(2026, time.April, 18, 18, 0, 0, 0, time.UTC)
	if err := store.writeScanHistory(context.Background(), []historySubmissionRecord{
		{
			ThreadDate: targetDate,
			UserID:     "345678901234567890",
			Guesses:    -1,
			Source:     "tracked-missing",
		},
		{
			ThreadDate:  targetDate,
			UserID:      "234567890123456789",
			Guesses:     4,
			SubmittedAt: &solvedAt,
			Source:      "tracked-submission",
		},
		{
			ThreadDate:  targetDate,
			UserID:      "456789012345678901",
			Guesses:     7,
			SubmittedAt: &missedAt,
			Source:      "tracked-submission",
		},
	}, historyReminderRecord{ThreadDate: targetDate, RemindedAt: &remindedAt}); err != nil {
		t.Fatalf("writeScanHistory() error = %v", err)
	}

	nextDate := targetDate.AddDate(0, 0, 1)
	nextReminderAt := remindedAt.Add(24 * time.Hour)
	nextSolvedAt := solvedAt.Add(24 * time.Hour)
	if err := store.writeScanHistory(context.Background(), []historySubmissionRecord{
		{
			ThreadDate:  nextDate,
			UserID:      "345678901234567890",
			Guesses:     3,
			SubmittedAt: &nextSolvedAt,
			Source:      "tracked-submission",
		},
	}, historyReminderRecord{ThreadDate: nextDate, RemindedAt: &nextReminderAt}); err != nil {
		t.Fatalf("writeScanHistory() next day error = %v", err)
	}

	originalSendChannelMessage := sendChannelMessageFn
	t.Cleanup(func() {
		sendChannelMessageFn = originalSendChannelMessage
	})
	sendChannelMessageFn = func(s *discordgo.Session, channelID, content string) (*discordgo.Message, error) {
		t.Fatal("sendChannelMessageFn() should not be called for --output stdout")
		return nil, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runCLI([]string{
		"discord-wordle-bot",
		reportStatsCommand,
		"--config", configPath,
		"--period", "daily",
		"--date", "2026-04-18",
		"--db-path", dbPath,
		"--output", "stdout",
	}, &stdout, &stderr, time.Now)
	if exitCode != exitSuccess {
		t.Fatalf("runCLI() exitCode = %d, want %d (stderr=%q)", exitCode, exitSuccess, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	report := stdout.String()
	if !strings.Contains(report, "Daily report for 2026-04-18 (America/New_York)") {
		t.Fatalf("stdout = %q, want daily report title", report)
	}
	if !strings.Contains(report, "User") || !strings.Contains(report, "Adj. Avg") || !strings.Contains(report, "Days Reminded") {
		t.Fatalf("stdout = %q, want stats table headers", report)
	}

	firstIndex := strings.Index(report, "345678901234567890")
	secondIndex := strings.Index(report, "234567890123456789")
	thirdIndex := strings.Index(report, "456789012345678901")
	if !(firstIndex >= 0 && secondIndex > firstIndex && thirdIndex > secondIndex) {
		t.Fatalf("stdout order = %q, want config order rows", report)
	}

	assertReportRow(t, report, "345678901234567890", []string{"345678901234567890", "—", "—", "—", "0", "7.00", "0"})
	assertReportRow(t, report, "234567890123456789", []string{"234567890123456789", "4", "4", "4.00", "0", "4.00", "1"})
	assertReportRow(t, report, "456789012345678901", []string{"456789012345678901", "—", "—", "—", "1", "7.00", "1"})
	if strings.Contains(report, "3.00") {
		t.Fatalf("stdout = %q, want next-day data excluded from daily report", report)
	}
}

func assertReportRow(t *testing.T, report, userID string, want []string) {
	t.Helper()

	for _, line := range strings.Split(report, "\n") {
		if !strings.HasPrefix(line, userID) {
			continue
		}
		if got := strings.Fields(line); len(got) == len(want) {
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("report row %q fields = %v, want %v", userID, got, want)
				}
			}
			return
		} else {
			t.Fatalf("report row %q fields = %v, want %v", userID, got, want)
		}
	}

	t.Fatalf("report row for %q not found in %q", userID, report)
}

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

	exitCode := runCLI([]string{"discord-wordle-bot", reportStatsCommand, "--config", configPath, "--period", "quarterly", "--date", "2026-04-18"}, &stdout, &stderr, time.Now)
	if exitCode != exitConfigError {
		t.Fatalf("runCLI() exitCode = %d, want %d", exitCode, exitConfigError)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), `configuration error: invalid --period "quarterly": supported values: daily, weekly, monthly, yearly`) {
		t.Fatalf("stderr = %q, want invalid-period error", stderr.String())
	}
}

func TestReportPeriodBounds(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}

	tests := []struct {
		name      string
		period    string
		date      string
		wantStart string
		wantEnd   string
	}{
		{
			name:      "daily",
			period:    reportPeriodDaily,
			date:      "2026-04-18",
			wantStart: "2026-04-18",
			wantEnd:   "2026-04-19",
		},
		{
			name:      "weekly",
			period:    reportPeriodWeekly,
			date:      "2026-04-15",
			wantStart: "2026-04-12",
			wantEnd:   "2026-04-19",
		},
		{
			name:      "monthly",
			period:    reportPeriodMonthly,
			date:      "2026-04-18",
			wantStart: "2026-04-01",
			wantEnd:   "2026-05-01",
		},
		{
			name:      "yearly",
			period:    reportPeriodYearly,
			date:      "2026-08-10",
			wantStart: "2026-01-01",
			wantEnd:   "2027-01-01",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetDate := mustParseReportStatsDate(t, tt.date, location)
			wantStart := mustParseReportStatsDate(t, tt.wantStart, location)
			wantEnd := mustParseReportStatsDate(t, tt.wantEnd, location)

			gotStart, gotEnd, err := reportPeriodBounds(tt.period, targetDate)
			if err != nil {
				t.Fatalf("reportPeriodBounds() error = %v", err)
			}
			if !gotStart.Equal(wantStart) {
				t.Fatalf("reportPeriodBounds() start = %s, want %s", gotStart, wantStart)
			}
			if !gotEnd.Equal(wantEnd) {
				t.Fatalf("reportPeriodBounds() end = %s, want %s", gotEnd, wantEnd)
			}
			if gotStart.Location().String() != location.String() {
				t.Fatalf("reportPeriodBounds() start location = %q, want %q", gotStart.Location(), location)
			}
			if gotEnd.Location().String() != location.String() {
				t.Fatalf("reportPeriodBounds() end location = %q, want %q", gotEnd.Location(), location)
			}
		})
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
	nextReminderAt := time.Date(2026, time.April, 19, 15, 0, 0, 0, time.UTC)
	nextSolvedAt := time.Date(2026, time.April, 19, 16, 0, 0, 0, time.UTC)
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

func TestRunReportStatsPeriodUsesContainingCalendarRange(t *testing.T) {
	const trackedUserID = "234567890123456789"

	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789"],
  "timezone": "America/New_York"
}`)

	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}

	tests := []struct {
		name          string
		period        string
		date          string
		title         string
		dayGuesses    map[string]int
		wantRowFields []string
	}{
		{
			name:   "weekly",
			period: reportPeriodWeekly,
			date:   "2026-04-15",
			title:  "Weekly report for 2026-04-15 (America/New_York)",
			dayGuesses: map[string]int{
				"2026-04-11": 1,
				"2026-04-12": 2,
				"2026-04-18": 6,
				"2026-04-19": 5,
			},
			wantRowFields: []string{trackedUserID, "2", "6", "4.00", "0", "4.00", "0"},
		},
		{
			name:   "monthly",
			period: reportPeriodMonthly,
			date:   "2026-04-18",
			title:  "Monthly report for 2026-04-18 (America/New_York)",
			dayGuesses: map[string]int{
				"2026-03-31": 1,
				"2026-04-01": 2,
				"2026-04-30": 6,
				"2026-05-01": 5,
			},
			wantRowFields: []string{trackedUserID, "2", "6", "4.00", "0", "4.00", "0"},
		},
		{
			name:   "yearly",
			period: reportPeriodYearly,
			date:   "2026-08-10",
			title:  "Yearly report for 2026-08-10 (America/New_York)",
			dayGuesses: map[string]int{
				"2025-12-31": 1,
				"2026-01-01": 2,
				"2026-12-31": 6,
				"2027-01-01": 5,
			},
			wantRowFields: []string{trackedUserID, "2", "6", "4.00", "0", "4.00", "0"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), "history.db")

			store, err := openHistoryStore(dbPath)
			if err != nil {
				t.Fatalf("openHistoryStore() error = %v", err)
			}
			defer store.Close()

			for day, guesses := range tt.dayGuesses {
				targetDate := mustParseReportStatsDate(t, day, location)
				submittedAt := time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 16, 0, 0, 0, time.UTC)
				if err := store.writeScanHistory(context.Background(), []historySubmissionRecord{
					{
						ThreadDate:  targetDate,
						UserID:      trackedUserID,
						Guesses:     guesses,
						SubmittedAt: &submittedAt,
						Source:      "tracked-submission",
					},
				}, historyReminderRecord{ThreadDate: targetDate}); err != nil {
					t.Fatalf("writeScanHistory(%s) error = %v", day, err)
				}
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
				"--period", tt.period,
				"--date", tt.date,
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
			if !strings.Contains(report, tt.title) {
				t.Fatalf("stdout = %q, want title %q", report, tt.title)
			}
			assertReportRow(t, report, trackedUserID, tt.wantRowFields)
		})
	}
}

func TestComputeUserStats(t *testing.T) {
	reminderAt := time.Date(2026, time.April, 18, 15, 0, 0, 0, time.UTC)
	beforeReminder := reminderAt.Add(-time.Hour)
	afterReminder := reminderAt.Add(time.Hour)
	nextReminderAt := reminderAt.AddDate(0, 0, 1)
	nextAfterReminder := nextReminderAt.Add(time.Hour)

	tests := []struct {
		name           string
		trackedUserIDs []string
		submissions    []periodSubmissionRow
		reminders      []periodReminderRow
		want           []wantUserPeriodStats
	}{
		{
			name:           "solves only",
			trackedUserIDs: []string{"user-b", "user-a"},
			submissions: []periodSubmissionRow{
				{ThreadDate: "2026-04-18", UserID: "user-a", Guesses: 5, SubmittedAt: &beforeReminder},
				{ThreadDate: "2026-04-19", UserID: "user-a", Guesses: 1, SubmittedAt: &nextAfterReminder},
				{ThreadDate: "2026-04-20", UserID: "user-a", Guesses: 3},
			},
			reminders: []periodReminderRow{
				{ThreadDate: "2026-04-18", RemindedAt: &reminderAt},
				{ThreadDate: "2026-04-19", RemindedAt: &nextReminderAt},
			},
			want: []wantUserPeriodStats{
				{UserID: "user-b", Best: "—", Worst: "—", Avg: "—", Misses: 0, AdjustedAvg: "—", DaysReminded: 0},
				{UserID: "user-a", Best: "1", Worst: "5", Avg: "3.00", Misses: 0, AdjustedAvg: "3.00", DaysReminded: 1},
			},
		},
		{
			name:           "misses only",
			trackedUserIDs: []string{"user-a"},
			submissions: []periodSubmissionRow{
				{ThreadDate: "2026-04-18", UserID: "user-a", Guesses: 7, SubmittedAt: &afterReminder},
				{ThreadDate: "2026-04-19", UserID: "user-a", Guesses: 7},
			},
			reminders: []periodReminderRow{
				{ThreadDate: "2026-04-18", RemindedAt: &reminderAt},
				{ThreadDate: "2026-04-19", RemindedAt: &nextReminderAt},
			},
			want: []wantUserPeriodStats{
				{UserID: "user-a", Best: "—", Worst: "—", Avg: "—", Misses: 2, AdjustedAvg: "7.00", DaysReminded: 1},
			},
		},
		{
			name:           "no entries only",
			trackedUserIDs: []string{"user-a"},
			submissions: []periodSubmissionRow{
				{ThreadDate: "2026-04-18", UserID: "user-a", Guesses: -1},
				{ThreadDate: "2026-04-19", UserID: "user-a", Guesses: -1},
			},
			reminders: []periodReminderRow{
				{ThreadDate: "2026-04-18", RemindedAt: &reminderAt},
				{ThreadDate: "2026-04-19", RemindedAt: &nextReminderAt},
			},
			want: []wantUserPeriodStats{
				{UserID: "user-a", Best: "—", Worst: "—", Avg: "—", Misses: 0, AdjustedAvg: "7.00", DaysReminded: 0},
			},
		},
		{
			name:           "mixed scores and reminders",
			trackedUserIDs: []string{"user-a", "user-b"},
			submissions: []periodSubmissionRow{
				{ThreadDate: "2026-04-18", UserID: "user-a", Guesses: 2, SubmittedAt: &afterReminder},
				{ThreadDate: "2026-04-19", UserID: "user-a", Guesses: 7, SubmittedAt: &beforeReminder},
				{ThreadDate: "2026-04-20", UserID: "user-a", Guesses: -1},
				{ThreadDate: "2026-04-21", UserID: "user-a", Guesses: 6, SubmittedAt: &nextAfterReminder},
				{ThreadDate: "2026-04-18", UserID: "ignored-user", Guesses: 1, SubmittedAt: &afterReminder},
			},
			reminders: []periodReminderRow{
				{ThreadDate: "2026-04-18", RemindedAt: &reminderAt},
				{ThreadDate: "2026-04-19", RemindedAt: &nextReminderAt},
				{ThreadDate: "2026-04-21"},
			},
			want: []wantUserPeriodStats{
				{UserID: "user-a", Best: "2", Worst: "6", Avg: "4.00", Misses: 1, AdjustedAvg: "5.50", DaysReminded: 1},
				{UserID: "user-b", Best: "—", Worst: "—", Avg: "—", Misses: 0, AdjustedAvg: "—", DaysReminded: 0},
			},
		},
		{
			name:           "zero period rows",
			trackedUserIDs: []string{"user-a"},
			want: []wantUserPeriodStats{
				{UserID: "user-a", Best: "—", Worst: "—", Avg: "—", Misses: 0, AdjustedAvg: "—", DaysReminded: 0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeUserStats(tt.trackedUserIDs, tt.submissions, tt.reminders)
			assertComputedUserStats(t, got, tt.want)
		})
	}
}

func mustParseReportStatsDate(t *testing.T, value string, location *time.Location) time.Time {
	t.Helper()

	targetDate, err := parseScanDate(value, location)
	if err != nil {
		t.Fatalf("parseScanDate(%q) error = %v", value, err)
	}
	return targetDate
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

type wantUserPeriodStats struct {
	UserID        string
	Best          string
	Worst         string
	Avg           string
	Misses        int
	AdjustedAvg   string
	DaysReminded  int
}

func assertComputedUserStats(t *testing.T, got []userPeriodStats, want []wantUserPeriodStats) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("computeUserStats() len = %d, want %d", len(got), len(want))
	}

	for i := range want {
		if got[i].UserID != want[i].UserID {
			t.Fatalf("computeUserStats()[%d].UserID = %q, want %q", i, got[i].UserID, want[i].UserID)
		}
		if gotBest := formatOptionalInt(got[i].Best); gotBest != want[i].Best {
			t.Fatalf("computeUserStats()[%d].Best = %q, want %q", i, gotBest, want[i].Best)
		}
		if gotWorst := formatOptionalInt(got[i].Worst); gotWorst != want[i].Worst {
			t.Fatalf("computeUserStats()[%d].Worst = %q, want %q", i, gotWorst, want[i].Worst)
		}
		if gotAvg := formatOptionalFloat(got[i].Avg); gotAvg != want[i].Avg {
			t.Fatalf("computeUserStats()[%d].Avg = %q, want %q", i, gotAvg, want[i].Avg)
		}
		if got[i].Misses != want[i].Misses {
			t.Fatalf("computeUserStats()[%d].Misses = %d, want %d", i, got[i].Misses, want[i].Misses)
		}
		if gotAdjustedAvg := formatOptionalFloat(got[i].AdjustedAvg); gotAdjustedAvg != want[i].AdjustedAvg {
			t.Fatalf("computeUserStats()[%d].AdjustedAvg = %q, want %q", i, gotAdjustedAvg, want[i].AdjustedAvg)
		}
		if got[i].DaysReminded != want[i].DaysReminded {
			t.Fatalf("computeUserStats()[%d].DaysReminded = %d, want %d", i, got[i].DaysReminded, want[i].DaysReminded)
		}
	}
}

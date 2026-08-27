package main

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestLoadConfigValidatesAndNormalizes(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "  Bot secret-token  ",
  "channel_id": "123456789012345678",
  "tracked_user_ids": [" 234567890123456789 ", "345678901234567890"],
  "timezone": "America/New_York"
}`)

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if cfg.BotToken != "secret-token" {
		t.Fatalf("BotToken = %q, want %q", cfg.BotToken, "secret-token")
	}
	if cfg.Location == nil || cfg.Location.String() != "America/New_York" {
		t.Fatalf("Location = %v, want America/New_York", cfg.Location)
	}
	if cfg.StarterPrompt != defaultStarterPrompt {
		t.Fatalf("StarterPrompt = %q, want %q", cfg.StarterPrompt, defaultStarterPrompt)
	}

	today := currentDay(time.Date(2026, time.April, 19, 2, 30, 0, 0, time.UTC), cfg.Location)
	if got := today.Format("Jan 2"); got != "Apr 18" {
		t.Fatalf("today.Format(\"Jan 2\") = %q, want %q", got, "Apr 18")
	}
}

func TestLoadConfigRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name:    "malformed json",
			config:  `{"bot_token":`,
			wantErr: "decode config",
		},
		{
			name: "multiple json objects",
			config: `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789"],
  "timezone": "America/New_York"
}{"extra":true}`,
			wantErr: "config file must contain exactly one JSON object",
		},
		{
			name: "missing channel id",
			config: `{
  "bot_token": "secret-token",
  "tracked_user_ids": ["234567890123456789"],
  "timezone": "America/New_York"
}`,
			wantErr: "channel_id is required",
		},
		{
			name: "invalid tracked user id",
			config: `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["not-a-snowflake"],
  "timezone": "America/New_York"
}`,
			wantErr: "tracked_user_ids[0] must be a Discord snowflake",
		},
		{
			name: "invalid timezone",
			config: `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789"],
  "timezone": "Mars/Phobos"
}`,
			wantErr: `invalid timezone "Mars/Phobos"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := writeTempConfig(t, tt.config)

			_, err := loadConfig(configPath)
			if err == nil {
				t.Fatal("loadConfig() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("loadConfig() error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestThreadTitleFormatsMonthAndDay(t *testing.T) {
	tests := []struct {
		name string
		at   time.Time
		want string
	}{
		{
			name: "single digit day",
			at:   time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC),
			want: "Jan 2",
		},
		{
			name: "double digit day",
			at:   time.Date(2026, time.November, 18, 0, 0, 0, 0, time.UTC),
			want: "Nov 18",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := threadTitle(tt.at); got != tt.want {
				t.Fatalf("threadTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateConfigCases(t *testing.T) {
	tests := []struct {
		name           string
		config         Config
		wantErr        string
		wantBotToken   string
		wantChannelID  string
		wantTrackedIDs []string
		wantPrompt     string
		wantTimezone   string
	}{
		{
			name: "normalizes valid config",
			config: Config{
				BotToken:       "  Bot secret-token  ",
				ChannelID:      "123456789012345678 ",
				TrackedUserIDs: []string{" 234567890123456789 ", "345678901234567890"},
				Timezone:       " America/New_York ",
			},
			wantBotToken:   "secret-token",
			wantChannelID:  "123456789012345678",
			wantTrackedIDs: []string{"234567890123456789", "345678901234567890"},
			wantPrompt:     defaultStarterPrompt,
			wantTimezone:   "America/New_York",
		},
		{
			name: "allows missing tracked users",
			config: Config{
				BotToken:  "secret-token",
				ChannelID: "123456789012345678",
				Timezone:  "America/New_York",
			},
			wantBotToken:   "secret-token",
			wantChannelID:  "123456789012345678",
			wantTrackedIDs: nil,
			wantPrompt:     defaultStarterPrompt,
			wantTimezone:   "America/New_York",
		},
		{
			name: "rejects invalid channel id",
			config: Config{
				BotToken:       "secret-token",
				ChannelID:      "not-a-snowflake",
				TrackedUserIDs: []string{"234567890123456789"},
				Timezone:       "America/New_York",
			},
			wantErr: "channel_id must be a Discord snowflake",
		},
		{
			name: "rejects invalid timezone",
			config: Config{
				BotToken:       "secret-token",
				ChannelID:      "123456789012345678",
				TrackedUserIDs: []string{"234567890123456789"},
				Timezone:       "Mars/Phobos",
			},
			wantErr: `invalid timezone "Mars/Phobos"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.config

			err := validateConfig(&cfg)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("validateConfig() error = nil, want non-nil")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("validateConfig() error = %q, want substring %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateConfig() error = %v", err)
			}

			if cfg.BotToken != tt.wantBotToken {
				t.Fatalf("BotToken = %q, want %q", cfg.BotToken, tt.wantBotToken)
			}
			if cfg.ChannelID != tt.wantChannelID {
				t.Fatalf("ChannelID = %q, want %q", cfg.ChannelID, tt.wantChannelID)
			}
			if !sameStrings(cfg.TrackedUserIDs, tt.wantTrackedIDs) {
				t.Fatalf("TrackedUserIDs = %v, want %v", cfg.TrackedUserIDs, tt.wantTrackedIDs)
			}
			if cfg.StarterPrompt != tt.wantPrompt {
				t.Fatalf("StarterPrompt = %q, want %q", cfg.StarterPrompt, tt.wantPrompt)
			}
			if cfg.Timezone != tt.wantTimezone {
				t.Fatalf("Timezone = %q, want %q", cfg.Timezone, tt.wantTimezone)
			}
			if cfg.Location == nil || cfg.Location.String() != tt.wantTimezone {
				t.Fatalf("Location = %v, want %q", cfg.Location, tt.wantTimezone)
			}
		})
	}
}

func TestCurrentDayUsesConfiguredLocation(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}

	tests := []struct {
		name     string
		now      time.Time
		location *time.Location
		want     string
	}{
		{
			name:     "new york previous calendar day",
			now:      time.Date(2026, time.April, 19, 2, 30, 0, 0, time.UTC),
			location: newYork,
			want:     "2026-04-18 22:30 EDT",
		},
		{
			name:     "tokyo next calendar day",
			now:      time.Date(2026, time.April, 18, 18, 30, 0, 0, time.UTC),
			location: tokyo,
			want:     "2026-04-19 03:30 JST",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := currentDay(tt.now, tt.location).Format("2006-01-02 15:04 MST"); got != tt.want {
				t.Fatalf("currentDay() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseScanDate(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}

	tests := []struct {
		name    string
		value   string
		want    string
		wantErr string
	}{
		{
			name:  "parses strict yyyy mm dd in configured timezone",
			value: "2026-04-18",
			want:  "2026-04-18 00:00 EDT",
		},
		{
			name:    "requires value",
			value:   "",
			wantErr: "--date is required",
		},
		{
			name:    "rejects non strict format",
			value:   "2026/04/18",
			wantErr: `invalid --date "2026/04/18": must use YYYY-MM-DD`,
		},
		{
			name:    "rejects impossible calendar date",
			value:   "2026-02-30",
			wantErr: `invalid --date "2026-02-30": must be a real calendar date in YYYY-MM-DD`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseScanDate(tt.value, location)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("parseScanDate() error = nil, want non-nil")
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("parseScanDate() error = %q, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseScanDate() error = %v", err)
			}
			if got.Format("2006-01-02 15:04 MST") != tt.want {
				t.Fatalf("parseScanDate() = %q, want %q", got.Format("2006-01-02 15:04 MST"), tt.want)
			}
		})
	}
}

func TestHistoryThreadMatchesUsesTitleAndCreationDay(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}

	targetDate, err := parseScanDate("2026-04-18", location)
	if err != nil {
		t.Fatalf("parseScanDate() error = %v", err)
	}

	matches, err := historyThreadMatches("archived", []*discordgo.Channel{
		{ID: discordSnowflakeID(time.Date(2025, time.April, 18, 16, 0, 0, 0, time.UTC)), Name: "Apr 18"},
		{ID: discordSnowflakeID(time.Date(2026, time.April, 19, 5, 0, 0, 0, time.UTC)), Name: "Apr 18"},
		{ID: discordSnowflakeID(time.Date(2026, time.April, 18, 16, 0, 0, 0, time.UTC)), Name: "Apr 18"},
		{ID: discordSnowflakeID(time.Date(2026, time.April, 18, 16, 0, 0, 0, time.UTC)), Name: "Apr 19"},
	}, targetDate, location)
	if err != nil {
		t.Fatalf("historyThreadMatches() error = %v", err)
	}

	if len(matches) != 1 {
		t.Fatalf("historyThreadMatches() len = %d, want 1", len(matches))
	}
	if matches[0].source != "archived" {
		t.Fatalf("historyThreadMatches() source = %q, want %q", matches[0].source, "archived")
	}
	wantID := discordSnowflakeID(time.Date(2026, time.April, 18, 16, 0, 0, 0, time.UTC))
	if matches[0].thread.ID != wantID {
		t.Fatalf("historyThreadMatches() thread ID = %q, want %q", matches[0].thread.ID, wantID)
	}
}

func TestFindTodayThread(t *testing.T) {
	today := time.Date(2026, time.April, 18, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		threads      []*discordgo.Channel
		wantThreadID string
		wantName     string
	}{
		{
			name: "finds matching thread after non-matches",
			threads: []*discordgo.Channel{
				nil,
				{ID: "old-thread-id", Name: "Apr 17"},
				{ID: "today-thread-id", Name: "Apr 18"},
			},
			wantThreadID: "today-thread-id",
			wantName:     "Apr 18",
		},
		{
			name: "returns empty when thread missing",
			threads: []*discordgo.Channel{
				{ID: "other-thread-id", Name: "Apr 19"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotThreadID, gotName := findTodayThread(tt.threads, today)
			if gotThreadID != tt.wantThreadID || gotName != tt.wantName {
				t.Fatalf("findTodayThread() = (%q, %q), want (%q, %q)", gotThreadID, gotName, tt.wantThreadID, tt.wantName)
			}
		})
	}
}

func TestSetupTodayThreadFindsExistingThread(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789"],
  "timezone": "America/New_York"
}`)

	originalNewDiscordSession := newDiscordSession
	originalListActiveThreads := listActiveThreadsFn
	t.Cleanup(func() {
		newDiscordSession = originalNewDiscordSession
		listActiveThreadsFn = originalListActiveThreads
	})

	newDiscordSession = func(botToken string) (*discordgo.Session, error) {
		if botToken != "secret-token" {
			t.Fatalf("newDiscordSession() botToken = %q, want %q", botToken, "secret-token")
		}
		return &discordgo.Session{}, nil
	}
	listActiveThreadsFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
		if channelID != "123456789012345678" {
			t.Fatalf("listActiveThreadsFn() channelID = %q, want %q", channelID, "123456789012345678")
		}
		return []*discordgo.Channel{{ID: "existing-thread-id", Name: "Apr 18"}}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	setup, exitCode := setupTodayThread(
		configPath,
		log.New(&stdout, "", 0),
		log.New(&stderr, "", 0),
		func() time.Time {
			return time.Date(2026, time.April, 19, 2, 30, 0, 0, time.UTC)
		},
	)

	if exitCode != exitSuccess {
		t.Fatalf("setupTodayThread() exitCode = %d, want %d", exitCode, exitSuccess)
	}
	if setup == nil {
		t.Fatal("setupTodayThread() setup = nil, want non-nil")
	}
	if setup.todayTitle != "Apr 18" {
		t.Fatalf("setupTodayThread() todayTitle = %q, want %q", setup.todayTitle, "Apr 18")
	}
	if setup.threadID != "existing-thread-id" || setup.threadName != "Apr 18" {
		t.Fatalf("setupTodayThread() thread = (%q, %q), want (%q, %q)", setup.threadID, setup.threadName, "existing-thread-id", "Apr 18")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "starting run for current_date=Apr 18 timezone=America/New_York") {
		t.Fatalf("stdout = %q, want start log", stdout.String())
	}
	if !strings.Contains(stdout.String(), `found active thread name="Apr 18" id=existing-thread-id`) {
		t.Fatalf("stdout = %q, want found-thread log", stdout.String())
	}
}

func TestIsQualifyingSubmission(t *testing.T) {
	tests := []struct {
		name    string
		message *discordgo.Message
		want    bool
	}{
		{
			name:    "nil message",
			message: nil,
			want:    false,
		},
		{
			name:    "missing author",
			message: &discordgo.Message{Content: "Wordle 123 4/6"},
			want:    false,
		},
		{
			name:    "empty author id",
			message: &discordgo.Message{Author: &discordgo.User{}, Content: "Wordle 123 4/6"},
			want:    false,
		},
		{
			name: "reply is ignored",
			message: &discordgo.Message{
				Author:           &discordgo.User{ID: "234567890123456789"},
				Content:          "Wordle 123 4/6",
				MessageReference: &discordgo.MessageReference{MessageID: "parent-id"},
			},
			want: false,
		},
		{
			name:    "top level wordle matches case insensitively",
			message: &discordgo.Message{Author: &discordgo.User{ID: "234567890123456789"}, Content: "  wOrDlE 123 4/6"},
			want:    true,
		},
		{
			name:    "top level scoredle matches",
			message: &discordgo.Message{Author: &discordgo.User{ID: "234567890123456789"}, Content: "Scoredle 42 streak"},
			want:    true,
		},
		{
			name:    "embedded wordle text does not match",
			message: &discordgo.Message{Author: &discordgo.User{ID: "234567890123456789"}, Content: "I did Wordle 123 4/6"},
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isQualifyingSubmission(tt.message); got != tt.want {
				t.Fatalf("isQualifyingSubmission() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCompletionStatus(t *testing.T) {
	tests := []struct {
		name         string
		tracked      []string
		messages     []*discordgo.Message
		wantComplete []string
		wantMissing  []string
	}{
		{
			name:    "qualifying posts mark tracked users complete in tracked order",
			tracked: []string{"234567890123456789", "345678901234567890", "456789012345678901"},
			messages: []*discordgo.Message{
				{Author: &discordgo.User{ID: "345678901234567890"}, Content: "Scoredle 123 4/6"},
				{Author: &discordgo.User{ID: "234567890123456789"}, Content: "Wordle 123 4/6"},
				{Author: &discordgo.User{ID: "999999999999999999"}, Content: "Wordle 999 1/6"},
			},
			wantComplete: []string{"234567890123456789", "345678901234567890"},
			wantMissing:  []string{"456789012345678901"},
		},
		{
			name:    "replies and non matching text stay missing",
			tracked: []string{"234567890123456789", "345678901234567890"},
			messages: []*discordgo.Message{
				{
					Author:           &discordgo.User{ID: "234567890123456789"},
					Content:          "Wordle 123 4/6",
					MessageReference: &discordgo.MessageReference{MessageID: "parent-id"},
				},
				{Author: &discordgo.User{ID: "345678901234567890"}, Content: "hello"},
				nil,
			},
			wantMissing: []string{"234567890123456789", "345678901234567890"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotComplete, gotMissing := completionStatus(tt.tracked, tt.messages)
			if !sameStrings(gotComplete, tt.wantComplete) {
				t.Fatalf("completionStatus() complete = %v, want %v", gotComplete, tt.wantComplete)
			}
			if !sameStrings(gotMissing, tt.wantMissing) {
				t.Fatalf("completionStatus() missing = %v, want %v", gotMissing, tt.wantMissing)
			}
		})
	}
}

func TestHasSameDayReminder(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation() error = %v", err)
	}

	today := time.Date(2026, time.April, 19, 2, 30, 0, 0, time.UTC)

	tests := []struct {
		name      string
		messages  []*discordgo.Message
		botUserID string
		want      bool
	}{
		{
			name:      "ignores nils and non-bot reminders",
			botUserID: "bot-user-id",
			messages: []*discordgo.Message{
				nil,
				{Content: "Reminder: <@1> still needs to post today's Wordle or Scoredle."},
				{Author: &discordgo.User{ID: "other-user-id"}, Content: "Reminder: <@1> still needs to post today's Wordle or Scoredle.", Timestamp: today},
			},
			want: false,
		},
		{
			name:      "suppresses duplicate on same local calendar day",
			botUserID: "bot-user-id",
			messages: []*discordgo.Message{
				{
					Author:    &discordgo.User{ID: "bot-user-id"},
					Content:   "Reminder: <@1> still needs to post today's Wordle or Scoredle.",
					Timestamp: time.Date(2026, time.April, 19, 1, 0, 0, 0, time.UTC),
				},
			},
			want: true,
		},
		{
			name:      "allows reminder from previous local calendar day",
			botUserID: "bot-user-id",
			messages: []*discordgo.Message{
				{
					Author:    &discordgo.User{ID: "bot-user-id"},
					Content:   "Reminder: <@1> still needs to post today's Wordle or Scoredle.",
					Timestamp: time.Date(2026, time.April, 18, 3, 0, 0, 0, time.UTC),
				},
			},
			want: false,
		},
		{
			name:      "ignores bot posts that are not reminders",
			botUserID: "bot-user-id",
			messages: []*discordgo.Message{
				{
					Author:    &discordgo.User{ID: "bot-user-id"},
					Content:   "hello world",
					Timestamp: today,
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasSameDayReminder(tt.messages, tt.botUserID, today, location); got != tt.want {
				t.Fatalf("hasSameDayReminder() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunCLIRequiresDateForScanHistory(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789"],
  "timezone": "America/New_York"
}`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runCLI([]string{"discord-wordle-bot", scanHistoryCommand, "--config", configPath}, &stdout, &stderr, time.Now)
	if exitCode != exitConfigError {
		t.Fatalf("runCLI() exitCode = %d, want %d", exitCode, exitConfigError)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "configuration error: --date is required") {
		t.Fatalf("stderr = %q, want missing-date error", stderr.String())
	}
}

func TestRunScanHistoryUsesConfiguredTimezoneAndFindsArchivedThread(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789"],
  "timezone": "America/New_York"
}`)
	dbPath := filepath.Join(t.TempDir(), "history.db")

	originalNewDiscordSession := newDiscordSession
	originalCurrentUser := currentUserFn
	originalListActiveThreads := listActiveThreadsFn
	originalListArchivedThreads := listArchivedThreadsFn
	originalMessagesInChannel := messagesInChannelFn
	t.Cleanup(func() {
		newDiscordSession = originalNewDiscordSession
		currentUserFn = originalCurrentUser
		listActiveThreadsFn = originalListActiveThreads
		listArchivedThreadsFn = originalListArchivedThreads
		messagesInChannelFn = originalMessagesInChannel
	})

	expectedThreadID := discordSnowflakeID(time.Date(2026, time.April, 18, 16, 0, 0, 0, time.UTC))

	newDiscordSession = func(botToken string) (*discordgo.Session, error) {
		return &discordgo.Session{}, nil
	}
	currentUserFn = func(s *discordgo.Session) (*discordgo.User, error) {
		return &discordgo.User{ID: "bot-user-id"}, nil
	}
	listActiveThreadsFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
		return []*discordgo.Channel{
			{ID: discordSnowflakeID(time.Date(2025, time.April, 18, 16, 0, 0, 0, time.UTC)), Name: "Apr 18"},
		}, nil
	}
	listArchivedThreadsFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
		return []*discordgo.Channel{
			{ID: expectedThreadID, Name: "Apr 18"},
		}, nil
	}
	messagesInChannelFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Message, error) {
		if channelID != expectedThreadID {
			t.Fatalf("messagesInChannelFn() channelID = %q, want %q", channelID, expectedThreadID)
		}
		return []*discordgo.Message{
			{Author: &discordgo.User{ID: "234567890123456789"}, Content: "Wordle 123 4/6"},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runCLI([]string{"discord-wordle-bot", scanHistoryCommand, "--config", configPath, "--date", "2026-04-18", "--db-path", dbPath}, &stdout, &stderr, time.Now)
	if exitCode != exitSuccess {
		t.Fatalf("runCLI() exitCode = %d, want %d", exitCode, exitSuccess)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "starting scan-history for target_date=Apr 18 timezone=America/New_York") {
		t.Fatalf("stdout = %q, want scan-history start log", stdout.String())
	}
	if !strings.Contains(stdout.String(), `found archived history thread name="Apr 18" id=`+expectedThreadID) {
		t.Fatalf("stdout = %q, want archived thread log", stdout.String())
	}
	if !strings.Contains(stdout.String(), "scan-history message summary complete=[234567890123456789] missing=[]") {
		t.Fatalf("stdout = %q, want scan message summary log", stdout.String())
	}
	if !strings.Contains(stdout.String(), `scan-history summary target_date=2026-04-18 thread_source=archived thread_id=`+expectedThreadID+` complete_count=1 missing_count=0 submission_rows=1 reminder_rows=1 reminder_timestamp_present=false`) {
		t.Fatalf("stdout = %q, want scan-history summary log", stdout.String())
	}
}

func TestRunScanHistoryFailsOnAmbiguousThread(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789"],
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

	firstID := discordSnowflakeID(time.Date(2026, time.April, 18, 16, 0, 0, 0, time.UTC))
	secondID := strconv.FormatInt(mustParseInt64(t, firstID)+1, 10)

	newDiscordSession = func(botToken string) (*discordgo.Session, error) {
		return &discordgo.Session{}, nil
	}
	listActiveThreadsFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
		return []*discordgo.Channel{
			{ID: firstID, Name: "Apr 18"},
		}, nil
	}
	listArchivedThreadsFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
		return []*discordgo.Channel{
			{ID: secondID, Name: "Apr 18"},
		}, nil
	}
	messagesInChannelFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Message, error) {
		t.Fatal("messagesInChannelFn() should not be called when thread resolution is ambiguous")
		return nil, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runCLI([]string{"discord-wordle-bot", scanHistoryCommand, "--config", configPath, "--date", "2026-04-18", "--db-path", dbPath}, &stdout, &stderr, time.Now)
	if exitCode != exitRuntimeError {
		t.Fatalf("runCLI() exitCode = %d, want %d", exitCode, exitRuntimeError)
	}
	if !strings.Contains(stderr.String(), "failed to resolve history thread: multiple threads matched 2026-04-18 (Apr 18):") {
		t.Fatalf("stderr = %q, want ambiguous-thread error", stderr.String())
	}
}

func TestRunScanHistoryFailsWhenResolvingBotUserForReminderScan(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789"],
  "timezone": "America/New_York"
}`)
	dbPath := filepath.Join(t.TempDir(), "history.db")

	originalNewDiscordSession := newDiscordSession
	originalCurrentUser := currentUserFn
	originalListActiveThreads := listActiveThreadsFn
	originalListArchivedThreads := listArchivedThreadsFn
	originalMessagesInChannel := messagesInChannelFn
	t.Cleanup(func() {
		newDiscordSession = originalNewDiscordSession
		currentUserFn = originalCurrentUser
		listActiveThreadsFn = originalListActiveThreads
		listArchivedThreadsFn = originalListArchivedThreads
		messagesInChannelFn = originalMessagesInChannel
	})

	expectedThreadID := discordSnowflakeID(time.Date(2026, time.April, 18, 16, 0, 0, 0, time.UTC))

	newDiscordSession = func(botToken string) (*discordgo.Session, error) {
		return &discordgo.Session{}, nil
	}
	currentUserFn = func(s *discordgo.Session) (*discordgo.User, error) {
		return nil, errors.New("boom")
	}
	listActiveThreadsFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
		return []*discordgo.Channel{{ID: expectedThreadID, Name: "Apr 18"}}, nil
	}
	listArchivedThreadsFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
		return nil, nil
	}
	messagesInChannelFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Message, error) {
		return []*discordgo.Message{
			{Author: &discordgo.User{ID: "234567890123456789"}, Content: "Wordle 123 4/6"},
		}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runCLI([]string{"discord-wordle-bot", scanHistoryCommand, "--config", configPath, "--date", "2026-04-18", "--db-path", dbPath}, &stdout, &stderr, time.Now)
	if exitCode != exitRuntimeError {
		t.Fatalf("runCLI() exitCode = %d, want %d", exitCode, exitRuntimeError)
	}
	if !strings.Contains(stderr.String(), "failed to resolve bot user for reminder scan: boom") {
		t.Fatalf("stderr = %q, want bot-user resolution error", stderr.String())
	}
}

func TestCompletionStatusUsesOnlyQualifyingTopLevelMessages(t *testing.T) {
	complete, missing := completionStatus(
		[]string{"234567890123456789", "345678901234567890", "456789012345678901"},
		[]*discordgo.Message{
			{Author: &discordgo.User{ID: "234567890123456789"}, Content: "  wordle 123 4/6"},
			{
				Author:           &discordgo.User{ID: "345678901234567890"},
				Content:          "Scoredle 42 streak",
				MessageReference: &discordgo.MessageReference{MessageID: "top-level-message-id"},
			},
			{Author: &discordgo.User{ID: "345678901234567890"}, Content: "   scoredle 42 streak"},
			{Author: &discordgo.User{ID: "456789012345678901"}, Content: "I did Wordle 123 4/6"},
			{Author: &discordgo.User{ID: "999999999999999999"}, Content: "Wordle 999 1/6"},
		},
	)

	if got, want := complete, []string{"234567890123456789", "345678901234567890"}; !sameStrings(got, want) {
		t.Fatalf("completionStatus() complete = %v, want %v", got, want)
	}
	if got, want := missing, []string{"456789012345678901"}; !sameStrings(got, want) {
		t.Fatalf("completionStatus() missing = %v, want %v", got, want)
	}
}

func TestFormatReminderMessageUsesNaturalMentions(t *testing.T) {
	tests := []struct {
		name    string
		missing []string
		want    string
	}{
		{
			name:    "one user",
			missing: []string{"234567890123456789"},
			want:    "Reminder: <@234567890123456789> still needs to post today's Wordle or Scoredle.",
		},
		{
			name:    "two users",
			missing: []string{"234567890123456789", "345678901234567890"},
			want:    "Reminder: <@234567890123456789> and <@345678901234567890> still need to post today's Wordle or Scoredle.",
		},
		{
			name:    "many users",
			missing: []string{"234567890123456789", "345678901234567890", "456789012345678901"},
			want:    "Reminder: <@234567890123456789>, <@345678901234567890>, and <@456789012345678901> still need to post today's Wordle or Scoredle.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatReminderMessage(tt.missing); got != tt.want {
				t.Fatalf("formatReminderMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	return configPath
}

func discordSnowflakeID(createdAt time.Time) string {
	const discordEpochMillis = int64(1420070400000)
	return strconv.FormatInt((createdAt.UnixMilli()-discordEpochMillis)<<22, 10)
}

func mustParseInt64(t *testing.T, value string) int64 {
	t.Helper()

	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("ParseInt() error = %v", err)
	}
	return parsed
}

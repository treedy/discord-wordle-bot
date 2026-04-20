package main

import (
	"bytes"
	"os"
	"path/filepath"
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
			name: "rejects missing tracked users",
			config: Config{
				BotToken:  "secret-token",
				ChannelID: "123456789012345678",
				Timezone:  "America/New_York",
			},
			wantErr: "tracked_user_ids must contain at least one user ID",
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

func TestRunUsesConfiguredTimezoneForDayAndLogsCronSafeSuccess(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789"],
  "timezone": "America/New_York"
}`)

	originalNewDiscordSession := newDiscordSession
	originalListActiveThreads := listActiveThreadsFn
	originalSendChannelMessage := sendChannelMessageFn
	originalCreateThreadFromMessage := createThreadFromMessageFn
	originalMessagesInChannel := messagesInChannelFn
	t.Cleanup(func() {
		newDiscordSession = originalNewDiscordSession
		listActiveThreadsFn = originalListActiveThreads
		sendChannelMessageFn = originalSendChannelMessage
		createThreadFromMessageFn = originalCreateThreadFromMessage
		messagesInChannelFn = originalMessagesInChannel
	})

	newDiscordSession = func(botToken string) (*discordgo.Session, error) {
		return &discordgo.Session{}, nil
	}
	listActiveThreadsFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
		return nil, nil
	}
	messagesInChannelCalled := false
	sendChannelMessageFn = func(s *discordgo.Session, channelID, content string) (*discordgo.Message, error) {
		if channelID != "123456789012345678" {
			t.Fatalf("sendChannelMessageFn() channelID = %q, want %q", channelID, "123456789012345678")
		}
		if content != defaultStarterPrompt {
			t.Fatalf("sendChannelMessageFn() content = %q, want %q", content, defaultStarterPrompt)
		}
		return &discordgo.Message{ID: "starter-message-id"}, nil
	}
	createThreadFromMessageFn = func(s *discordgo.Session, channelID, messageID, name string) (*discordgo.Channel, error) {
		if channelID != "123456789012345678" {
			t.Fatalf("createThreadFromMessageFn() channelID = %q, want %q", channelID, "123456789012345678")
		}
		if messageID != "starter-message-id" {
			t.Fatalf("createThreadFromMessageFn() messageID = %q, want %q", messageID, "starter-message-id")
		}
		if name != "Apr 18" {
			t.Fatalf("createThreadFromMessageFn() name = %q, want %q", name, "Apr 18")
		}
		return &discordgo.Channel{ID: "new-thread-id", Name: name}, nil
	}
	messagesInChannelFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Message, error) {
		messagesInChannelCalled = true
		return nil, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(configPath, &stdout, &stderr, func() time.Time {
		return time.Date(2026, time.April, 19, 2, 30, 0, 0, time.UTC)
	})

	if exitCode != exitSuccess {
		t.Fatalf("run() exitCode = %d, want %d", exitCode, exitSuccess)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	logOutput := stdout.String()
	if !strings.Contains(logOutput, "current_date=Apr 18 timezone=America/New_York") {
		t.Fatalf("stdout = %q, want timezone-based thread date log", logOutput)
	}
	if !strings.Contains(logOutput, `created daily thread name="Apr 18"; exiting without reminder`) {
		t.Fatalf("stdout = %q, want created-thread success log", logOutput)
	}
	if messagesInChannelCalled {
		t.Fatal("messagesInChannelFn() should not be called when thread is created during this run")
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

func TestRunPostsReminderForMissingTrackedUsersInExistingThread(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789", "345678901234567890"],
  "timezone": "America/New_York"
}`)

	originalNewDiscordSession := newDiscordSession
	originalCurrentUser := currentUserFn
	originalListActiveThreads := listActiveThreadsFn
	originalSendChannelMessage := sendChannelMessageFn
	originalCreateThreadFromMessage := createThreadFromMessageFn
	originalMessagesInChannel := messagesInChannelFn
	t.Cleanup(func() {
		newDiscordSession = originalNewDiscordSession
		currentUserFn = originalCurrentUser
		listActiveThreadsFn = originalListActiveThreads
		sendChannelMessageFn = originalSendChannelMessage
		createThreadFromMessageFn = originalCreateThreadFromMessage
		messagesInChannelFn = originalMessagesInChannel
	})

	newDiscordSession = func(botToken string) (*discordgo.Session, error) {
		return &discordgo.Session{}, nil
	}
	currentUserFn = func(s *discordgo.Session) (*discordgo.User, error) {
		return &discordgo.User{ID: "bot-user-id"}, nil
	}
	listActiveThreadsFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
		return []*discordgo.Channel{{ID: "existing-thread-id", Name: "Apr 18"}}, nil
	}
	createThreadFromMessageCalled := false
	createThreadFromMessageFn = func(s *discordgo.Session, channelID, messageID, name string) (*discordgo.Channel, error) {
		createThreadFromMessageCalled = true
		return nil, nil
	}
	messagesInChannelFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Message, error) {
		if channelID != "existing-thread-id" {
			t.Fatalf("messagesInChannelFn() channelID = %q, want %q", channelID, "existing-thread-id")
		}
		return []*discordgo.Message{
			{
				Author:           &discordgo.User{ID: "234567890123456789"},
				Content:          "Scordle 123 4/6",
				MessageReference: &discordgo.MessageReference{MessageID: "another-message-id"},
			},
			{Author: &discordgo.User{ID: "234567890123456789"}, Content: " Wordle 123 4/6"},
			{Author: &discordgo.User{ID: "999999999999999999"}, Content: "hello"},
		}, nil
	}
	sendChannelMessageFn = func(s *discordgo.Session, channelID, content string) (*discordgo.Message, error) {
		if channelID != "existing-thread-id" {
			t.Fatalf("sendChannelMessageFn() channelID = %q, want %q", channelID, "existing-thread-id")
		}
		if content != "Reminder: <@345678901234567890> still needs to post today's Wordle or Scoredle." {
			t.Fatalf("sendChannelMessageFn() content = %q", content)
		}
		return &discordgo.Message{ID: "reminder-message-id"}, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(configPath, &stdout, &stderr, func() time.Time {
		return time.Date(2026, time.April, 19, 2, 30, 0, 0, time.UTC)
	})

	if exitCode != exitSuccess {
		t.Fatalf("run() exitCode = %d, want %d", exitCode, exitSuccess)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), `found active thread name="Apr 18" id=existing-thread-id`) {
		t.Fatalf("stdout = %q, want found-thread log", stdout.String())
	}
	if !strings.Contains(stdout.String(), "computed completion complete=[234567890123456789] missing=[345678901234567890]") {
		t.Fatalf("stdout = %q, want completion log", stdout.String())
	}
	if !strings.Contains(stdout.String(), "posted reminder for missing=[345678901234567890]") {
		t.Fatalf("stdout = %q, want posted-reminder log", stdout.String())
	}
	if createThreadFromMessageCalled {
		t.Fatal("createThreadFromMessageFn() should not be called when today's thread already exists")
	}
}

func TestRunSkipsReminderWhenNoTrackedUsersAreMissing(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789", "345678901234567890"],
  "timezone": "America/New_York"
}`)

	originalNewDiscordSession := newDiscordSession
	originalCurrentUser := currentUserFn
	originalListActiveThreads := listActiveThreadsFn
	originalSendChannelMessage := sendChannelMessageFn
	originalMessagesInChannel := messagesInChannelFn
	t.Cleanup(func() {
		newDiscordSession = originalNewDiscordSession
		currentUserFn = originalCurrentUser
		listActiveThreadsFn = originalListActiveThreads
		sendChannelMessageFn = originalSendChannelMessage
		messagesInChannelFn = originalMessagesInChannel
	})

	newDiscordSession = func(botToken string) (*discordgo.Session, error) {
		return &discordgo.Session{}, nil
	}
	currentUserCalled := false
	currentUserFn = func(s *discordgo.Session) (*discordgo.User, error) {
		currentUserCalled = true
		return &discordgo.User{ID: "bot-user-id"}, nil
	}
	listActiveThreadsFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
		return []*discordgo.Channel{{ID: "existing-thread-id", Name: "Apr 18"}}, nil
	}
	messagesInChannelFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Message, error) {
		return []*discordgo.Message{
			{Author: &discordgo.User{ID: "234567890123456789"}, Content: "Wordle 123 4/6"},
			{Author: &discordgo.User{ID: "345678901234567890"}, Content: "Scoredle 123 4/6"},
		}, nil
	}
	sendChannelMessageFn = func(s *discordgo.Session, channelID, content string) (*discordgo.Message, error) {
		t.Fatal("sendChannelMessageFn() should not be called when nobody is missing")
		return nil, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(configPath, &stdout, &stderr, func() time.Time {
		return time.Date(2026, time.April, 19, 2, 30, 0, 0, time.UTC)
	})

	if exitCode != exitSuccess {
		t.Fatalf("run() exitCode = %d, want %d", exitCode, exitSuccess)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if currentUserCalled {
		t.Fatal("currentUserFn() should not be called when nobody is missing")
	}
	if !strings.Contains(stdout.String(), "no tracked users missing; skipping reminder") {
		t.Fatalf("stdout = %q, want skip-reminder log", stdout.String())
	}
}

func TestRunSuppressesDuplicateSameDayReminder(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789", "345678901234567890"],
  "timezone": "America/New_York"
}`)

	originalNewDiscordSession := newDiscordSession
	originalCurrentUser := currentUserFn
	originalListActiveThreads := listActiveThreadsFn
	originalSendChannelMessage := sendChannelMessageFn
	originalMessagesInChannel := messagesInChannelFn
	t.Cleanup(func() {
		newDiscordSession = originalNewDiscordSession
		currentUserFn = originalCurrentUser
		listActiveThreadsFn = originalListActiveThreads
		sendChannelMessageFn = originalSendChannelMessage
		messagesInChannelFn = originalMessagesInChannel
	})

	newDiscordSession = func(botToken string) (*discordgo.Session, error) {
		return &discordgo.Session{}, nil
	}
	currentUserFn = func(s *discordgo.Session) (*discordgo.User, error) {
		return &discordgo.User{ID: "bot-user-id"}, nil
	}
	listActiveThreadsFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
		return []*discordgo.Channel{{ID: "existing-thread-id", Name: "Apr 18"}}, nil
	}
	messagesInChannelFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Message, error) {
		return []*discordgo.Message{
			{Author: &discordgo.User{ID: "234567890123456789"}, Content: "Wordle 123 4/6"},
			{
				Author:    &discordgo.User{ID: "bot-user-id"},
				Content:   "Reminder: <@345678901234567890> still needs to post today's Wordle or Scoredle.",
				Timestamp: time.Date(2026, time.April, 18, 16, 0, 0, 0, time.UTC),
			},
		}, nil
	}
	sendChannelMessageFn = func(s *discordgo.Session, channelID, content string) (*discordgo.Message, error) {
		t.Fatal("sendChannelMessageFn() should not be called when a same-day reminder already exists")
		return nil, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := run(configPath, &stdout, &stderr, func() time.Time {
		return time.Date(2026, time.April, 19, 2, 30, 0, 0, time.UTC)
	})

	if exitCode != exitSuccess {
		t.Fatalf("run() exitCode = %d, want %d", exitCode, exitSuccess)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "same-day reminder already exists in thread; skipping duplicate reminder") {
		t.Fatalf("stdout = %q, want duplicate-suppression log", stdout.String())
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

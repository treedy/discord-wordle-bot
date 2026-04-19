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

func TestRunPostsReminderForMissingUsersInExistingThread(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789", "345678901234567890"],
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
			{Author: &discordgo.User{ID: "234567890123456789"}, Content: "Wordle 123 4/6"},
			{Author: &discordgo.User{ID: "999999999999999999"}, Content: "hello"},
		}, nil
	}
	sendChannelMessageFn = func(s *discordgo.Session, channelID, content string) (*discordgo.Message, error) {
		if channelID != "existing-thread-id" {
			t.Fatalf("sendChannelMessageFn() channelID = %q, want %q", channelID, "existing-thread-id")
		}
		if content != "Hey <@345678901234567890>! You haven't completed Wordle today" {
			t.Fatalf("sendChannelMessageFn() content = %q, want %q", content, "Hey <@345678901234567890>! You haven't completed Wordle today")
		}
		return &discordgo.Message{ID: "reminder-id"}, nil
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
	if !strings.Contains(stdout.String(), "posted reminder for 1 missing user(s)") {
		t.Fatalf("stdout = %q, want reminder log", stdout.String())
	}
	if createThreadFromMessageCalled {
		t.Fatal("createThreadFromMessageFn() should not be called when today's thread already exists")
	}
}

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	return configPath
}

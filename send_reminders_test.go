package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestRunSendRemindersPostsReminderForMissingTrackedUsersInExistingThread(t *testing.T) {
	configPath := writeTempConfig(t, `{
  "bot_token": "secret-token",
  "channel_id": "123456789012345678",
  "tracked_user_ids": ["234567890123456789", "345678901234567890"],
  "timezone": "America/New_York"
}`)

	originalNewDiscordSession := newDiscordSession
	originalCurrentUser := currentUserFn
	originalListActiveThreads := listActiveThreadsFn
	originalCreateThread := createThreadFn
	originalSendChannelMessage := sendChannelMessageFn
	originalMessagesInChannel := messagesInChannelFn
	t.Cleanup(func() {
		newDiscordSession = originalNewDiscordSession
		currentUserFn = originalCurrentUser
		listActiveThreadsFn = originalListActiveThreads
		createThreadFn = originalCreateThread
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
	createThreadCalled := false
	createThreadFn = func(s *discordgo.Session, channelID, name string) (*discordgo.Channel, error) {
		createThreadCalled = true
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

	exitCode := runSendReminders(configPath, defaultHistoryDBPath, &stdout, &stderr, func() time.Time {
		return time.Date(2026, time.April, 19, 2, 30, 0, 0, time.UTC)
	})

	if exitCode != exitSuccess {
		t.Fatalf("runSendReminders() exitCode = %d, want %d", exitCode, exitSuccess)
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
	if createThreadCalled {
		t.Fatal("createThreadFn() should not be called when today's thread already exists")
	}
}

func TestRunSendRemindersSkipsReminderWhenNoTrackedUsersAreMissing(t *testing.T) {
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

	exitCode := runSendReminders(configPath, defaultHistoryDBPath, &stdout, &stderr, func() time.Time {
		return time.Date(2026, time.April, 19, 2, 30, 0, 0, time.UTC)
	})

	if exitCode != exitSuccess {
		t.Fatalf("runSendReminders() exitCode = %d, want %d", exitCode, exitSuccess)
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

func TestRunSendRemindersSuppressesDuplicateSameDayReminder(t *testing.T) {
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

	exitCode := runSendReminders(configPath, defaultHistoryDBPath, &stdout, &stderr, func() time.Time {
		return time.Date(2026, time.April, 19, 2, 30, 0, 0, time.UTC)
	})

	if exitCode != exitSuccess {
		t.Fatalf("runSendReminders() exitCode = %d, want %d", exitCode, exitSuccess)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if !strings.Contains(stdout.String(), "same-day reminder already exists in thread; skipping duplicate reminder") {
		t.Fatalf("stdout = %q, want duplicate-suppression log", stdout.String())
	}
}

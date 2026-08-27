package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestCreateDailyThread(t *testing.T) {
	originalCreateThread := createThreadFn
	originalSendChannelMessage := sendChannelMessageFn
	t.Cleanup(func() {
		createThreadFn = originalCreateThread
		sendChannelMessageFn = originalSendChannelMessage
	})

	tests := []struct {
		name           string
		createErr      error
		sendErr        error
		wantErr        string
		wantLogOp      string
		wantSendTo     string
		wantThreadID   string
		wantThreadName string
	}{
		{
			name:           "creates thread and posts starter prompt",
			wantSendTo:     "new-thread-id",
			wantThreadID:   "new-thread-id",
			wantThreadName: "Apr 18",
		},
		{
			name:      "returns create thread error",
			createErr: errors.New("thread create failed"),
			wantErr:   "create thread: thread create failed",
			wantLogOp: "create thread",
		},
		{
			name:       "returns send starter prompt error",
			sendErr:    errors.New("starter prompt failed"),
			wantErr:    "send starter prompt: starter prompt failed",
			wantLogOp:  "send starter prompt",
			wantSendTo: "new-thread-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			createThreadFn = func(s *discordgo.Session, channelID, name string) (*discordgo.Channel, error) {
				if channelID != "123456789012345678" {
					t.Fatalf("createThreadFn() channelID = %q, want %q", channelID, "123456789012345678")
				}
				if name != "Apr 18" {
					t.Fatalf("createThreadFn() name = %q, want %q", name, "Apr 18")
				}
				if tt.createErr != nil {
					return nil, tt.createErr
				}
				return &discordgo.Channel{ID: "new-thread-id", Name: name}, nil
			}

			sendCalled := false
			sendChannelMessageFn = func(s *discordgo.Session, channelID, content string) (*discordgo.Message, error) {
				sendCalled = true
				if channelID != tt.wantSendTo {
					t.Fatalf("sendChannelMessageFn() channelID = %q, want %q", channelID, tt.wantSendTo)
				}
				if content != defaultStarterPrompt {
					t.Fatalf("sendChannelMessageFn() content = %q, want %q", content, defaultStarterPrompt)
				}
				if tt.sendErr != nil {
					return nil, tt.sendErr
				}
				return &discordgo.Message{ID: "starter-message-id"}, nil
			}

			thread, err := createDailyThread(&discordgo.Session{}, "123456789012345678", "Apr 18", defaultStarterPrompt)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("createDailyThread() error = nil, want non-nil")
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("createDailyThread() error = %q, want %q", err, tt.wantErr)
				}
				var threadErr *dailyThreadError
				if !errors.As(err, &threadErr) {
					t.Fatalf("createDailyThread() error type = %T, want *dailyThreadError", err)
				}
				if threadErr.op != tt.wantLogOp {
					t.Fatalf("dailyThreadError.op = %q, want %q", threadErr.op, tt.wantLogOp)
				}
				if tt.createErr != nil && sendCalled {
					t.Fatal("sendChannelMessageFn() should not be called when thread creation fails")
				}
				return
			}

			if err != nil {
				t.Fatalf("createDailyThread() error = %v", err)
			}
			if thread == nil {
				t.Fatal("createDailyThread() thread = nil, want non-nil")
			}
			if thread.ID != tt.wantThreadID {
				t.Fatalf("createDailyThread() thread ID = %q, want %q", thread.ID, tt.wantThreadID)
			}
			if thread.Name != tt.wantThreadName {
				t.Fatalf("createDailyThread() thread Name = %q, want %q", thread.Name, tt.wantThreadName)
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
	originalCreateThread := createThreadFn
	originalSendChannelMessage := sendChannelMessageFn
	originalMessagesInChannel := messagesInChannelFn
	t.Cleanup(func() {
		newDiscordSession = originalNewDiscordSession
		listActiveThreadsFn = originalListActiveThreads
		createThreadFn = originalCreateThread
		sendChannelMessageFn = originalSendChannelMessage
		messagesInChannelFn = originalMessagesInChannel
	})

	newDiscordSession = func(botToken string) (*discordgo.Session, error) {
		return &discordgo.Session{}, nil
	}
	listActiveThreadsFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
		return nil, nil
	}
	createThreadFn = func(s *discordgo.Session, channelID, name string) (*discordgo.Channel, error) {
		if channelID != "123456789012345678" {
			t.Fatalf("createThreadFn() channelID = %q, want %q", channelID, "123456789012345678")
		}
		if name != "Apr 18" {
			t.Fatalf("createThreadFn() name = %q, want %q", name, "Apr 18")
		}
		return &discordgo.Channel{ID: "new-thread-id", Name: name}, nil
	}
	messagesInChannelCalled := false
	sendChannelMessageFn = func(s *discordgo.Session, channelID, content string) (*discordgo.Message, error) {
		if channelID != "new-thread-id" {
			t.Fatalf("sendChannelMessageFn() channelID = %q, want %q", channelID, "new-thread-id")
		}
		if content != defaultStarterPrompt {
			t.Fatalf("sendChannelMessageFn() content = %q, want %q", content, defaultStarterPrompt)
		}
		return &discordgo.Message{ID: "starter-message-id"}, nil
	}
	messagesInChannelFn = func(s *discordgo.Session, channelID string) ([]*discordgo.Message, error) {
		messagesInChannelCalled = true
		return nil, nil
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runCreateThread(configPath, defaultHistoryDBPath, &stdout, &stderr, func() time.Time {
		return time.Date(2026, time.April, 19, 2, 30, 0, 0, time.UTC)
	})

	if exitCode != exitSuccess {
		t.Fatalf("runCreateThread() exitCode = %d, want %d", exitCode, exitSuccess)
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

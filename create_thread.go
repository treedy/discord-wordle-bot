package main

import (
	"errors"
	"io"
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
)

type dailyThreadError struct {
	op  string
	err error
}

func (e *dailyThreadError) Error() string {
	return e.op + ": " + e.err.Error()
}

func (e *dailyThreadError) Unwrap() error {
	return e.err
}

func createDailyThread(s *discordgo.Session, channelID, threadName, starterPrompt string) (*discordgo.Channel, error) {
	threadChannel, err := createThreadFn(s, channelID, threadName)
	if err != nil {
		return nil, &dailyThreadError{op: "create thread", err: err}
	}

	if _, err := sendChannelMessageFn(s, threadChannel.ID, starterPrompt); err != nil {
		return nil, &dailyThreadError{op: "send starter prompt", err: err}
	}

	return threadChannel, nil
}

func runCreateThread(cfgPath, dbPath string, stdout, stderr io.Writer, now func() time.Time) int {
	// dbPath is reserved for parity with other run* helpers and future use.
	_ = dbPath

	infoLogger := log.New(stdout, "", log.LstdFlags)
	errorLogger := log.New(stderr, "", log.LstdFlags)

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		errorLogger.Printf("configuration error: %v", err)
		return exitConfigError
	}

	today := currentDay(now(), cfg.Location)
	todayTitle := threadTitle(today)
	infoLogger.Printf("starting run for current_date=%s timezone=%s", todayTitle, cfg.Timezone)

	dg, err := newDiscordSession(cfg.BotToken)
	if err != nil {
		errorLogger.Printf("failed to create discord session: %v", err)
		return exitRuntimeError
	}

	threads, err := listActiveThreadsFn(dg, cfg.ChannelID)
	if err != nil {
		errorLogger.Printf("failed to list active threads: %v", err)
		return exitRuntimeError
	}

	threadID, threadName := findTodayThread(threads, today)
	if threadID != "" {
		infoLogger.Printf("found active thread name=%q id=%s", threadName, threadID)
		return exitSuccess
	}

	if _, err := createDailyThread(dg, cfg.ChannelID, todayTitle, cfg.StarterPrompt); err != nil {
		var threadErr *dailyThreadError
		if errors.As(err, &threadErr) && threadErr.op == "send starter prompt" {
			errorLogger.Printf("failed to send thread starter message: %v", threadErr.err)
			return exitRuntimeError
		}
		errorLogger.Printf("failed to create daily thread: %v", err)
		return exitRuntimeError
	}

	infoLogger.Printf("created daily thread name=%q; exiting without reminder", todayTitle)
	return exitSuccess
}

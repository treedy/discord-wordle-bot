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

	setup, exitCode := setupTodayThread(cfgPath, infoLogger, errorLogger, now)
	if exitCode != exitSuccess {
		return exitCode
	}
	if setup.threadID != "" {
		return exitSuccess
	}

	if _, err := createDailyThread(setup.dg, setup.cfg.ChannelID, setup.todayTitle, setup.cfg.StarterPrompt); err != nil {
		var threadErr *dailyThreadError
		if errors.As(err, &threadErr) && threadErr.op == "send starter prompt" {
			errorLogger.Printf("failed to send thread starter message: %v", threadErr.err)
			return exitRuntimeError
		}
		errorLogger.Printf("failed to create daily thread: %v", err)
		return exitRuntimeError
	}

	infoLogger.Printf("created daily thread name=%q; exiting without reminder", setup.todayTitle)
	return exitSuccess
}

package main

import (
	"context"
	"io"
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
)

func runSendReminders(cfgPath, dbPath string, stdout, stderr io.Writer, now func() time.Time) int {
	infoLogger := log.New(stdout, "", log.LstdFlags)
	errorLogger := log.New(stderr, "", log.LstdFlags)

	setup, exitCode := setupTodayThread(cfgPath, infoLogger, errorLogger, now)
	if exitCode != exitSuccess {
		return exitCode
	}
	if setup.threadID == "" {
		errorLogger.Printf("no active thread for today (%s); run create-thread first", setup.todayTitle)
		return exitRuntimeError
	}

	return runSendRemindersForThread(setup.cfg, resolveDBPath(cfgPath, dbPath), setup.dg, setup.threadID, setup.today, infoLogger, errorLogger)
}

func runSendRemindersForThread(cfg *Config, dbPath string, dg *discordgo.Session, threadID string, today time.Time, infoLogger, errorLogger *log.Logger) int {
	msgs, err := messagesInChannelFn(dg, threadID)
	if err != nil {
		errorLogger.Printf("failed to fetch messages in thread: %v", err)
		return exitRuntimeError
	}

	store, err := openHistoryStore(dbPath)
	if err != nil {
		errorLogger.Printf("failed to open history store: %v", err)
		return exitRuntimeError
	}
	defer store.Close()

	users, err := store.listUsers(context.Background())
	if err != nil {
		errorLogger.Printf("failed to load users from DB: %v", err)
		return exitRuntimeError
	}

	trackedIDs := make([]string, 0, len(users))
	for _, u := range users {
		trackedIDs = append(trackedIDs, u.UserID)
	}
	if len(trackedIDs) == 0 {
		trackedIDs = cfg.TrackedUserIDs
	}

	complete, missing := completionStatus(trackedIDs, msgs)
	infoLogger.Printf("computed completion complete=%v missing=%v", complete, missing)

	if len(missing) == 0 {
		infoLogger.Printf("no tracked users missing; skipping reminder")
		return exitSuccess
	}

	currentUser, err := currentUserFn(dg)
	if err != nil {
		errorLogger.Printf("failed to resolve current bot user: %v", err)
		return exitRuntimeError
	}

	if hasSameDayReminder(msgs, currentUser.ID, today, cfg.Location) {
		infoLogger.Printf("same-day reminder already exists in thread; skipping duplicate reminder")
		return exitSuccess
	}

	reminder := formatReminderMessage(missing)
	if _, err := sendChannelMessageFn(dg, threadID, reminder); err != nil {
		errorLogger.Printf("failed to post reminder: %v", err)
		return exitRuntimeError
	}

	infoLogger.Printf("posted reminder for missing=%v", missing)
	return exitSuccess
}

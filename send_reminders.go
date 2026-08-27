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
	if threadID == "" {
		errorLogger.Printf("no active thread for today (%s); run create-thread first", todayTitle)
		return exitRuntimeError
	}

	infoLogger.Printf("found active thread name=%q id=%s", threadName, threadID)
	return runSendRemindersForThread(cfg, resolveDBPath(cfgPath, dbPath), dg, threadID, today, infoLogger, errorLogger)
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

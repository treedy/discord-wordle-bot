package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

type Config struct {
	BotToken       string         `json:"bot_token"`
	ChannelID      string         `json:"channel_id"`
	TrackedUserIDs []string       `json:"tracked_user_ids"`
	StarterPrompt  string         `json:"starter_prompt"`
	Timezone       string         `json:"timezone"`
	Location       *time.Location `json:"-"`
}

const (
	defaultStarterPrompt = "Enter your Wordle score here"
	exitSuccess          = 0
	exitRuntimeError     = 1
	exitConfigError      = 2

	createThreadCommand  = "create-thread"
	sendRemindersCommand = "send-reminders"
	scanHistoryCommand   = "scan-history"
	reportStatsCommand   = "report-stats"

	scanDateLayout          = "2006-01-02"
	archivedThreadsPageSize = 100
)

var (
	discordIDPattern  = regexp.MustCompile(`^\d+$`)
	submissionPattern = regexp.MustCompile(`(?i)^\s*(Wordle|Scoredle)`)
	reminderPattern   = regexp.MustCompile(`(?i)^\s*Reminder:`)
	scanDatePattern   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	newDiscordSession = func(botToken string) (*discordgo.Session, error) {
		return discordgo.New("Bot " + botToken)
	}
	currentUserFn = func(s *discordgo.Session) (*discordgo.User, error) {
		return s.User("@me")
	}
	listActiveThreadsFn   = listActiveThreads
	listArchivedThreadsFn = listArchivedThreads
	createThreadFn        = func(s *discordgo.Session, channelID, name string) (*discordgo.Channel, error) {
		return s.ThreadStart(channelID, name, discordgo.ChannelTypeGuildPublicThread, 1440)
	}
	messagesInChannelFn  = messagesInChannel
	sendChannelMessageFn = func(s *discordgo.Session, channelID, content string) (*discordgo.Message, error) {
		return s.ChannelMessageSend(channelID, content)
	}
)

func loadConfig(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	var c Config
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&c); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode config: config file must contain exactly one JSON object")
		}
		return nil, fmt.Errorf("decode config: invalid trailing data: %w", err)
	}

	if err := validateConfig(&c); err != nil {
		return nil, err
	}

	return &c, nil
}

func validateConfig(c *Config) error {
	c.BotToken = normalizeBotToken(c.BotToken)
	c.ChannelID = strings.TrimSpace(c.ChannelID)
	c.StarterPrompt = strings.TrimSpace(c.StarterPrompt)
	c.Timezone = strings.TrimSpace(c.Timezone)

	if c.BotToken == "" {
		return errors.New("bot_token is required")
	}
	if c.ChannelID == "" {
		return errors.New("channel_id is required")
	}
	if !discordIDPattern.MatchString(c.ChannelID) {
		return fmt.Errorf("channel_id must be a Discord snowflake")
	}
	// tracked_user_ids is now optional; tracked users are loaded from the
	// database Users table for runtime operations. If present, validate and
	// normalize but do not require it.
	if len(c.TrackedUserIDs) > 0 {
		normalizedUserIDs := make([]string, 0, len(c.TrackedUserIDs))
		for i, id := range c.TrackedUserIDs {
			id = strings.TrimSpace(id)
			if id == "" {
				return fmt.Errorf("tracked_user_ids[%d] is required", i)
			}
			if !discordIDPattern.MatchString(id) {
				return fmt.Errorf("tracked_user_ids[%d] must be a Discord snowflake", i)
			}
			normalizedUserIDs = append(normalizedUserIDs, id)
		}
		c.TrackedUserIDs = normalizedUserIDs
	}

	if c.StarterPrompt == "" {
		c.StarterPrompt = defaultStarterPrompt
	}

	if c.Timezone == "" {
		return errors.New("timezone is required")
	}
	location, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return fmt.Errorf("invalid timezone %q: %w", c.Timezone, err)
	}
	c.Location = location

	return nil
}

func normalizeBotToken(token string) string {
	token = strings.TrimSpace(token)
	return strings.TrimPrefix(token, "Bot ")
}

func resolveDBPath(cfgPath, dbPath string) string {
	if dbPath != defaultHistoryDBPath {
		return dbPath
	}
	if cfgPath == "" {
		return dbPath
	}
	return filepath.Join(filepath.Dir(cfgPath), dbPath)
}

func currentDay(now time.Time, location *time.Location) time.Time {
	return now.In(location)
}

func listActiveThreads(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
	threads, err := s.ThreadsActive(channelID)
	if err != nil {
		return nil, err
	}
	if threads == nil {
		return nil, nil
	}
	return threads.Threads, nil
}

func listArchivedThreads(s *discordgo.Session, channelID string) ([]*discordgo.Channel, error) {
	var all []*discordgo.Channel
	var before *time.Time

	for {
		threads, err := s.ThreadsArchived(channelID, before, archivedThreadsPageSize)
		if err != nil {
			return nil, err
		}
		if threads == nil || len(threads.Threads) == 0 {
			return all, nil
		}

		all = append(all, threads.Threads...)
		if !threads.HasMore {
			return all, nil
		}

		nextBefore, ok := archivedThreadsBefore(threads.Threads)
		if !ok {
			return nil, errors.New("cannot paginate archived threads without archive timestamps")
		}
		before = &nextBefore
	}
}

func archivedThreadsBefore(threads []*discordgo.Channel) (time.Time, bool) {
	var oldest time.Time
	found := false
	for _, thread := range threads {
		if thread == nil || thread.ThreadMetadata == nil || thread.ThreadMetadata.ArchiveTimestamp.IsZero() {
			continue
		}
		if !found || thread.ThreadMetadata.ArchiveTimestamp.Before(oldest) {
			oldest = thread.ThreadMetadata.ArchiveTimestamp
			found = true
		}
	}
	return oldest, found
}

func findTodayThread(threads []*discordgo.Channel, t time.Time) (string, string) {
	want := threadTitle(t)
	for _, th := range threads {
		if th != nil && th.Name == want {
			return th.ID, th.Name
		}
	}
	return "", ""
}

type todayThreadSetup struct {
	cfg        *Config
	today      time.Time
	todayTitle string
	dg         *discordgo.Session
	threadID   string
	threadName string
}

func threadTitle(t time.Time) string {
	return t.Format("Jan 2")
}

func parseScanDate(value string, location *time.Location) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("--date is required")
	}
	if !scanDatePattern.MatchString(value) {
		return time.Time{}, fmt.Errorf("invalid --date %q: must use YYYY-MM-DD", value)
	}

	targetDate, err := time.ParseInLocation(scanDateLayout, value, location)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --date %q: must be a real calendar date in YYYY-MM-DD", value)
	}
	return targetDate, nil
}

func messagesInChannel(s *discordgo.Session, channelID string) ([]*discordgo.Message, error) {
	// fetch up to 200 messages using pagination (in most threads that's enough)
	var all []*discordgo.Message
	before := ""
	for {
		msgs, err := s.ChannelMessages(channelID, 100, before, "", "")
		if err != nil {
			return nil, err
		}
		if len(msgs) == 0 {
			break
		}
		all = append(all, msgs...)
		if len(msgs) < 100 {
			break
		}
		before = msgs[len(msgs)-1].ID
	}
	return all, nil
}

func isTopLevelMessage(m *discordgo.Message) bool {
	return m != nil && (m.MessageReference == nil || m.MessageReference.MessageID == "")
}

func isQualifyingSubmission(m *discordgo.Message) bool {
	return m != nil &&
		m.Author != nil &&
		m.Author.ID != "" &&
		isTopLevelMessage(m) &&
		submissionPattern.MatchString(m.Content)
}

func completionStatus(trackedUserIDs []string, msgs []*discordgo.Message) ([]string, []string) {
	posted := make(map[string]bool)
	for _, m := range msgs {
		if isQualifyingSubmission(m) {
			posted[m.Author.ID] = true
		}
	}

	complete := make([]string, 0, len(trackedUserIDs))
	missing := make([]string, 0, len(trackedUserIDs))
	for _, id := range trackedUserIDs {
		if posted[id] {
			complete = append(complete, id)
			continue
		}
		missing = append(missing, id)
	}

	return complete, missing
}

func formatUserMentions(userIDs []string) string {
	if len(userIDs) == 0 {
		return ""
	}

	mentions := make([]string, 0, len(userIDs))
	for _, id := range userIDs {
		mentions = append(mentions, "<@"+id+">")
	}

	switch len(mentions) {
	case 1:
		return mentions[0]
	case 2:
		return mentions[0] + " and " + mentions[1]
	default:
		return strings.Join(mentions[:len(mentions)-1], ", ") + ", and " + mentions[len(mentions)-1]
	}
}

func formatReminderMessage(missingUserIDs []string) string {
	mentions := formatUserMentions(missingUserIDs)
	if len(missingUserIDs) == 1 {
		return fmt.Sprintf("Reminder: %s still needs to post today's Wordle or Scoredle.", mentions)
	}
	return fmt.Sprintf("Reminder: %s still need to post today's Wordle or Scoredle.", mentions)
}

func sameCalendarDay(a, b time.Time, location *time.Location) bool {
	ay, am, ad := a.In(location).Date()
	by, bm, bd := b.In(location).Date()
	return ay == by && am == bm && ad == bd
}

func hasSameDayReminder(msgs []*discordgo.Message, botUserID string, today time.Time, location *time.Location) bool {
	for _, m := range msgs {
		if m == nil || m.Author == nil {
			continue
		}
		if m.Author.ID != botUserID {
			continue
		}
		if !reminderPattern.MatchString(m.Content) {
			continue
		}
		if !sameCalendarDay(m.Timestamp, today, location) {
			continue
		}
		return true
	}
	return false
}

type historyThreadMatch struct {
	thread  *discordgo.Channel
	source  string
	created time.Time
}

func historyThreadMatches(source string, threads []*discordgo.Channel, target time.Time, location *time.Location) ([]historyThreadMatch, error) {
	wantTitle := threadTitle(target)
	matches := make([]historyThreadMatch, 0)

	for _, thread := range threads {
		if thread == nil || thread.Name != wantTitle {
			continue
		}

		createdAt, err := discordgo.SnowflakeTimestamp(thread.ID)
		if err != nil {
			return nil, fmt.Errorf("resolve creation time for thread %q (%s): %w", thread.Name, thread.ID, err)
		}
		if !sameCalendarDay(createdAt, target, location) {
			continue
		}

		matches = append(matches, historyThreadMatch{
			thread:  thread,
			source:  source,
			created: createdAt,
		})
	}

	return matches, nil
}

func resolveHistoryThread(s *discordgo.Session, channelID string, target time.Time, location *time.Location) (*historyThreadMatch, error) {
	activeThreads, err := listActiveThreadsFn(s, channelID)
	if err != nil {
		return nil, fmt.Errorf("list active threads: %w", err)
	}
	activeMatches, err := historyThreadMatches("active", activeThreads, target, location)
	if err != nil {
		return nil, err
	}

	archivedThreads, err := listArchivedThreadsFn(s, channelID)
	if err != nil {
		return nil, fmt.Errorf("list archived threads: %w", err)
	}
	archivedMatches, err := historyThreadMatches("archived", archivedThreads, target, location)
	if err != nil {
		return nil, err
	}

	matches := append(activeMatches, archivedMatches...)
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no thread found for %s (%s)", target.Format(scanDateLayout), threadTitle(target))
	case 1:
		return &matches[0], nil
	default:
		ids := make([]string, 0, len(matches))
		for _, match := range matches {
			ids = append(ids, match.thread.ID)
		}
		sort.Strings(ids)
		return nil, fmt.Errorf("multiple threads matched %s (%s): %s", target.Format(scanDateLayout), threadTitle(target), strings.Join(ids, ", "))
	}
}

func main() {
	os.Exit(runCLI(os.Args, os.Stdout, os.Stderr, time.Now))
}

func runCLI(args []string, stdout, stderr io.Writer, now func() time.Time) int {
	programName := "discord-wordle-bot"
	if len(args) > 0 && args[0] != "" {
		programName = args[0]
	}
	if len(args) < 2 {
		usage(stderr, programName)
		return exitConfigError
	}

	cmd := args[1]

	const configFilePath = "config.json"
	configFilePathDesc := fmt.Sprintf("path to %s", configFilePath)

	createCmd := flag.NewFlagSet(createThreadCommand, flag.ContinueOnError)
	createCmd.SetOutput(stderr)
	createCfg := createCmd.String("config", configFilePath, configFilePathDesc)

	addUserCmd := flag.NewFlagSet("add-user", flag.ContinueOnError)
	addUserCmd.SetOutput(stderr)
	addUserCfg := addUserCmd.String("config", configFilePath, configFilePathDesc)
	addUserID := addUserCmd.String("id", "", "discord user id to add")
	addUserName := addUserCmd.String("name", "", "display name for the user")
	addUserDBPath := addUserCmd.String("db-path", defaultHistoryDBPath, "path to SQLite scan-history database")

	sendCmd := flag.NewFlagSet(sendRemindersCommand, flag.ContinueOnError)
	sendCmd.SetOutput(stderr)
	sendCfg := sendCmd.String("config", configFilePath, configFilePathDesc)

	scanCmd := flag.NewFlagSet(scanHistoryCommand, flag.ContinueOnError)
	scanCmd.SetOutput(stderr)
	scanCfg := scanCmd.String("config", configFilePath, configFilePathDesc)
	scanDate := scanCmd.String("date", "", "target date in YYYY-MM-DD")
	scanDBPath := scanCmd.String("db-path", defaultHistoryDBPath, "path to SQLite scan-history database")

	reportCmd := flag.NewFlagSet(reportStatsCommand, flag.ContinueOnError)
	reportCmd.SetOutput(stderr)
	reportCfg := reportCmd.String("config", configFilePath, configFilePathDesc)
	reportPeriod := reportCmd.String("period", "", "reporting period")
	reportDate := reportCmd.String("date", "", "target date in YYYY-MM-DD")
	reportDBPath := reportCmd.String("db-path", defaultHistoryDBPath, "path to SQLite scan-history database")
	reportOutput := reportCmd.String("output", reportOutputBoth, "report output destination")

	switch cmd {
	case "help", "-h", "--help":
		usage(stdout, programName)
		return exitSuccess
	case createThreadCommand:
		if err := createCmd.Parse(args[2:]); err != nil {
			return exitConfigError
		}
		return runCreateThread(*createCfg, defaultHistoryDBPath, stdout, stderr, now)
	case sendRemindersCommand:
		if err := sendCmd.Parse(args[2:]); err != nil {
			return exitConfigError
		}
		return runSendReminders(*sendCfg, defaultHistoryDBPath, stdout, stderr, now)
	case scanHistoryCommand:
		if err := scanCmd.Parse(args[2:]); err != nil {
			return exitConfigError
		}
		return runScanHistory(*scanCfg, *scanDate, *scanDBPath, stdout, stderr)
	case "add-user":
		if err := addUserCmd.Parse(args[2:]); err != nil {
			return exitConfigError
		}
		return runAddUser(*addUserCfg, *addUserID, *addUserName, *addUserDBPath, stdout, stderr)
	case reportStatsCommand:
		if err := reportCmd.Parse(args[2:]); err != nil {
			return exitConfigError
		}
		return runReportStats(*reportCfg, *reportPeriod, *reportDate, *reportDBPath, *reportOutput, stdout, stderr)
	default:
		usage(stderr, programName)
		return exitConfigError
	}
}

func usage(w io.Writer, programName string) {
	fmt.Fprintf(w, "Usage: %s <command> [options]\n\n", programName)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintf(w, "  %s  Create today's thread if it doesn't exist\n", createThreadCommand)
	fmt.Fprintf(w, "  %s  Send reminders for missing users in today's thread\n", sendRemindersCommand)
	fmt.Fprintf(w, "  %s  Scan a specific day's thread history\n", scanHistoryCommand)
	fmt.Fprintf(w, "  %s  Add a tracked user to the database (flags: --id, --name, --db-path)\n", "add-user")
	fmt.Fprintf(w, "  %s  Print a statistics report from stored history\n", reportStatsCommand)
	fmt.Fprintln(w, "  help            Show this help message")
}

func runScanHistory(cfgPath, dateValue, dbPath string, stdout, stderr io.Writer) int {
	infoLogger := log.New(stdout, "", log.LstdFlags)
	errorLogger := log.New(stderr, "", log.LstdFlags)

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		errorLogger.Printf("configuration error: %v", err)
		return exitConfigError
	}

	targetDate, err := parseScanDate(dateValue, cfg.Location)
	if err != nil {
		errorLogger.Printf("configuration error: %v", err)
		return exitConfigError
	}

	targetTitle := threadTitle(targetDate)
	infoLogger.Printf("starting scan-history for target_date=%s timezone=%s db_path=%s", targetTitle, cfg.Timezone, dbPath)

	dg, err := newDiscordSession(cfg.BotToken)
	if err != nil {
		errorLogger.Printf("failed to create discord session: %v", err)
		return exitRuntimeError
	}

	store, err := openHistoryStore(resolveDBPath(cfgPath, dbPath))
	if err != nil {
		errorLogger.Printf("failed to open history store: %v", err)
		return exitRuntimeError
	}
	defer store.Close()

	match, err := resolveHistoryThread(dg, cfg.ChannelID, targetDate, cfg.Location)
	if err != nil {
		errorLogger.Printf("failed to resolve history thread: %v", err)
		return exitRuntimeError
	}
	infoLogger.Printf("found %s history thread name=%q id=%s", match.source, match.thread.Name, match.thread.ID)

	msgs, err := messagesInChannelFn(dg, match.thread.ID)
	if err != nil {
		errorLogger.Printf("failed to fetch messages in history thread id=%s: %v", match.thread.ID, err)
		return exitRuntimeError
	}

	// load tracked users from DB (fall back to config if empty)
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
		// fallback to config-based tracked IDs for compatibility
		trackedIDs = cfg.TrackedUserIDs
	}

	complete, missing := completionStatus(trackedIDs, msgs)
	infoLogger.Printf("scan-history message summary complete=%v missing=%v", complete, missing)

	currentUser, err := currentUserFn(dg)
	if err != nil {
		errorLogger.Printf("failed to resolve bot user for reminder scan: %v", err)
		return exitRuntimeError
	}

	submissions, reminder := buildScanHistoryRecords(targetDate, trackedIDs, currentUser.ID, cfg.Location, msgs)
	if err := store.writeScanHistory(context.Background(), submissions, reminder); err != nil {
		errorLogger.Printf("failed to persist scan history for thread id=%s: %v", match.thread.ID, err)
		return exitRuntimeError
	}
	infoLogger.Printf("persisted scan history submission_rows=%d reminder_rows=1 reminder_timestamp_present=%v", len(submissions), reminder.RemindedAt != nil)
	infoLogger.Printf("scan-history summary target_date=%s thread_source=%s thread_id=%s complete_count=%d missing_count=%d submission_rows=%d reminder_rows=1 reminder_timestamp_present=%v", targetDate.Format(scanDateLayout), match.source, match.thread.ID, len(complete), len(missing), len(submissions), reminder.RemindedAt != nil)
	return exitSuccess
}

func setupTodayThread(cfgPath string, infoLogger, errorLogger *log.Logger, now func() time.Time) (*todayThreadSetup, int) {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		errorLogger.Printf("configuration error: %v", err)
		return nil, exitConfigError
	}

	today := currentDay(now(), cfg.Location)
	todayTitle := threadTitle(today)
	infoLogger.Printf("starting run for current_date=%s timezone=%s", todayTitle, cfg.Timezone)

	dg, err := newDiscordSession(cfg.BotToken)
	if err != nil {
		errorLogger.Printf("failed to create discord session: %v", err)
		return nil, exitRuntimeError
	}

	threads, err := listActiveThreadsFn(dg, cfg.ChannelID)
	if err != nil {
		errorLogger.Printf("failed to list active threads: %v", err)
		return nil, exitRuntimeError
	}

	threadID, threadName := findTodayThread(threads, today)
	if threadID != "" {
		infoLogger.Printf("found active thread name=%q id=%s", threadName, threadID)
	}

	return &todayThreadSetup{
		cfg:        cfg,
		today:      today,
		todayTitle: todayTitle,
		dg:         dg,
		threadID:   threadID,
		threadName: threadName,
	}, exitSuccess
}

func runAddUser(cfgPath, userID, displayName, dbPath string, stdout, stderr io.Writer) int {
	errorLogger := log.New(stderr, "", log.LstdFlags)
	infoLogger := log.New(stdout, "", log.LstdFlags)

	userID = strings.TrimSpace(userID)
	displayName = strings.TrimSpace(displayName)
	if userID == "" || displayName == "" {
		errorLogger.Printf("--id and --name are required")
		return exitConfigError
	}
	if !discordIDPattern.MatchString(userID) {
		errorLogger.Printf("invalid --id %q: must be a Discord snowflake", userID)
		return exitConfigError
	}

	store, err := openHistoryStore(resolveDBPath(cfgPath, dbPath))
	if err != nil {
		errorLogger.Printf("failed to open history store: %v", err)
		return exitRuntimeError
	}
	defer store.Close()

	if err := store.addUser(context.Background(), userID, displayName); err != nil {
		errorLogger.Printf("failed to add user: %v", err)
		return exitRuntimeError
	}

	infoLogger.Printf("added user id=%s name=%q", userID, displayName)
	return exitSuccess
}

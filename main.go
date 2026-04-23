package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"regexp"
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
)

var (
	discordIDPattern  = regexp.MustCompile(`^\d+$`)
	submissionPattern = regexp.MustCompile(`(?i)^\s*(Wordle|Scoredle)`)
	reminderPattern   = regexp.MustCompile(`(?i)^\s*Reminder:`)
	newDiscordSession = func(botToken string) (*discordgo.Session, error) {
		return discordgo.New("Bot " + botToken)
	}
	currentUserFn = func(s *discordgo.Session) (*discordgo.User, error) {
		return s.User("@me")
	}
	listActiveThreadsFn  = listActiveThreads
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
	if len(c.TrackedUserIDs) == 0 {
		return errors.New("tracked_user_ids must contain at least one user ID")
	}

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

func findTodayThread(threads []*discordgo.Channel, t time.Time) (string, string) {
	want := threadTitle(t)
	for _, th := range threads {
		if th != nil && th.Name == want {
			return th.ID, th.Name
		}
	}
	return "", ""
}

func threadTitle(t time.Time) string {
	return t.Format("Jan 2")
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

func main() {
	cfgPath := flag.String("config", "config.json", "path to config.json")
	flag.Parse()

	os.Exit(run(*cfgPath, os.Stdout, os.Stderr, time.Now))
}

func run(cfgPath string, stdout, stderr io.Writer, now func() time.Time) int {
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

	// If we don't find today's thread, create the thread then send 1 message to it
	threadID, threadName := findTodayThread(threads, today)
	if threadID == "" {
		threadChannel, err := dg.ThreadStart(cfg.ChannelID, todayTitle, discordgo.ChannelTypeGuildPublicThread, 1440)
		if err != nil {
			errorLogger.Printf("failed to create daily thread: %v", err)
			return exitRuntimeError
		}
		if _, err := dg.ChannelMessageSend(threadChannel.ID, cfg.StarterPrompt); err != nil {
			errorLogger.Printf("failed to send thread starter message: %v", err)
			return exitRuntimeError
		}
		infoLogger.Printf("created daily thread name=%q; exiting without reminder", todayTitle)
		return exitSuccess
	}
	infoLogger.Printf("found active thread name=%q id=%s", threadName, threadID)

	msgs, err := messagesInChannelFn(dg, threadID)
	if err != nil {
		errorLogger.Printf("failed to fetch messages in thread: %v", err)
		return exitRuntimeError
	}

	complete, missing := completionStatus(cfg.TrackedUserIDs, msgs)
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

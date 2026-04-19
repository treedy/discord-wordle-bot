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
	Timezone       string         `json:"timezone"`
	Location       *time.Location `json:"-"`
}

const (
	exitSuccess      = 0
	exitRuntimeError = 1
	exitConfigError  = 2
)

var (
	discordIDPattern  = regexp.MustCompile(`^\d+$`)
	submissionPattern = regexp.MustCompile(`(?i)^\s*(Wordle|Scordle)`)
	newDiscordSession = func(botToken string) (*discordgo.Session, error) {
		return discordgo.New("Bot " + botToken)
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
	want := t.Format("Jan 2")
	for _, th := range threads {
		if th != nil && th.Name == want {
			return th.ID, th.Name
		}
	}
	return "", ""
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
	infoLogger.Printf("starting run for current_date=%s timezone=%s", today.Format("Jan 2"), cfg.Timezone)

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
		infoLogger.Printf("no active thread found for current_date=%s; exiting without reminder", today.Format("Jan 2"))
		return exitSuccess
	}
	infoLogger.Printf("found active thread name=%q id=%s", threadName, threadID)

	msgs, err := messagesInChannelFn(dg, threadID)
	if err != nil {
		errorLogger.Printf("failed to fetch messages in thread: %v", err)
		return exitRuntimeError
	}

	posted := make(map[string]bool)
	for _, m := range msgs {
		if m.Author == nil || m.Author.ID == "" {
			continue
		}
		if submissionPattern.MatchString(m.Content) {
			posted[m.Author.ID] = true
		}
	}

	var missing []string
	for _, id := range cfg.TrackedUserIDs {
		if !posted[id] {
			missing = append(missing, id)
		}
	}

	if len(missing) == 0 {
		infoLogger.Print("all tracked users have already posted; exiting without reminder")
		return exitSuccess
	}

	mentions := make([]string, len(missing))
	for i, id := range missing {
		mentions[i] = fmt.Sprintf("<@%s>", id)
	}

	var mentionStr string
	switch len(mentions) {
	case 1:
		mentionStr = mentions[0]
	case 2:
		mentionStr = mentions[0] + " and " + mentions[1]
	default:
		mentionStr = strings.Join(mentions[:len(mentions)-1], ", ") + ", and " + mentions[len(mentions)-1]
	}

	content := fmt.Sprintf("Hey %s! You haven't completed Wordle today", mentionStr)
	if _, err := sendChannelMessageFn(dg, threadID, content); err != nil {
		errorLogger.Printf("failed to send reminder message: %v", err)
		return exitRuntimeError
	}

	infoLogger.Printf("posted reminder for %d missing user(s)", len(missing))
	return exitSuccess
}

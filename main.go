package main

import (
    "encoding/json"
    "errors"
    "flag"
    "fmt"
    "io"
    "net/http"
    "os"
    "regexp"
    "strings"
    "time"

    "github.com/bwmarrin/discordgo"
)

type Config struct {
    BotToken      string   `json:"bot_token"`
    ChannelID     string   `json:"channel_id"`
    TrackedUserIDs []string `json:"tracked_user_ids"`
    Timezone      string   `json:"timezone"` // currently ignored; uses local
}

func loadConfig(path string) (*Config, error) {
    f, err := os.Open(path)
    if err != nil {
        return nil, err
    }
    defer f.Close()
    b, err := io.ReadAll(f)
    if err != nil {
        return nil, err
    }
    var c Config
    if err := json.Unmarshal(b, &c); err != nil {
        return nil, err
    }
    if c.BotToken == "" || c.ChannelID == "" || len(c.TrackedUserIDs) == 0 {
        return nil, errors.New("bot_token, channel_id and tracked_user_ids are required in config")
    }
    return &c, nil
}

// call Discord REST to list active threads for a channel
func listActiveThreads(channelID, botToken string) ([]map[string]interface{}, error) {
    url := fmt.Sprintf("https://discord.com/api/v10/channels/%s/threads/active", channelID)
    req, _ := http.NewRequest("GET", url, nil)
    req.Header.Set("Authorization", "Bot "+strings.TrimPrefix(botToken, "Bot "))
    req.Header.Set("User-Agent", "discord-wordle-bot")
    client := &http.Client{Timeout: 15 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    if resp.StatusCode != 200 {
        body, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("discord API returned %d: %s", resp.StatusCode, string(body))
    }
    var out struct {
        Threads []map[string]interface{} `json:"threads"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
        return nil, err
    }
    return out.Threads, nil
}

func findTodayThread(threads []map[string]interface{}, t time.Time) (string, string) {
    want := t.Format("Jan 2")
    for _, th := range threads {
        if nameI, ok := th["name"]; ok {
            if name, ok2 := nameI.(string); ok2 && name == want {
                if idI, ok3 := th["id"]; ok3 {
                    if id, ok4 := idI.(string); ok4 {
                        return id, name
                    }
                }
            }
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

    cfg, err := loadConfig(*cfgPath)
    if err != nil {
        fmt.Fprintln(os.Stderr, "failed to load config:", err)
        os.Exit(2)
    }

    dg, err := discordgo.New("Bot " + strings.TrimPrefix(cfg.BotToken, "Bot "))
    if err != nil {
        fmt.Fprintln(os.Stderr, "failed to create discord session:", err)
        os.Exit(2)
    }
    // open connection (needed for REST methods in discordgo reliably)
    if err := dg.Open(); err != nil {
        fmt.Fprintln(os.Stderr, "failed to open discord session:", err)
        os.Exit(2)
    }
    defer dg.Close()

    now := time.Now()

    threads, err := listActiveThreads(cfg.ChannelID, cfg.BotToken)
    if err != nil {
        fmt.Fprintln(os.Stderr, "failed to list active threads:", err)
        os.Exit(2)
    }

    threadID, threadName := findTodayThread(threads, now)
    if threadID == "" {
        fmt.Println("no active thread found for today (", now.Format("Jan 2"), ")")
        os.Exit(0)
    }
    fmt.Println("found thread:", threadName, "id=", threadID)

    msgs, err := messagesInChannel(dg, threadID)
    if err != nil {
        fmt.Fprintln(os.Stderr, "failed to fetch messages in thread:", err)
        os.Exit(2)
    }

    // regex: case-insensitive, anchored, allow leading whitespace
    re := regexp.MustCompile(`(?i)^\s*(Wordle|Scordle)`)
    posted := make(map[string]bool)
    for _, m := range msgs {
        if m.Author == nil || m.Author.ID == "" {
            continue
        }
        if re.MatchString(m.Content) {
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
        fmt.Println("no missing users — everyone posted")
        os.Exit(0)
    }

    // build mention list with commas and 'and'
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

    _, err = dg.ChannelMessageSend(threadID, content)
    if err != nil {
        fmt.Fprintln(os.Stderr, "failed to send reminder message:", err)
        os.Exit(2)
    }
    fmt.Println("posted reminder to thread")
}

# Discord Wordle Notifier (Go)

A small command-line tool that checks a configured channel for an active daily thread (title like "Apr 25") and posts a reminder into the thread for tracked users who haven't posted a message starting with "Wordle" or "Scordle".

Quick start

1. Copy `config.sample.json` to `config.json` and fill in `bot_token`, `channel_id`, and `tracked_user_ids`.
2. Build the program:

```bash
go build -o discord-wordle-bot ./
```

3. Run from cron or manually:

```bash
./discord-wordle-bot --config config.json
```
# discord-wordle-bot
A bot for discord that reminds users to enter their Wordle for the day and other useful chores

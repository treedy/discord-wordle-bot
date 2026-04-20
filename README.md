# Discord Wordle Notifier (Go)

A small command-line tool that checks a configured channel for an active daily thread (title like "Apr 25") and posts a reminder into the thread for tracked users who haven't posted a message starting with "Wordle" or "Scordle".

Quick start

1. Copy `config.sample.json` to `config.json` and fill in `bot_token`, `channel_id`, `starter_prompt`, `tracked_user_ids`, and `timezone` (for example `America/New_York`).
2. Build the program:

```bash
go build -o discord-wordle-bot ./
```

3. Run from cron or manually:

```bash
./discord-wordle-bot --config config.json
```

4. (Optional) Build and run with Docker

Build the image:

```bash
docker build -t discord-wordle-bot .
```

Run the container (mount your local `config.json` over the container config):

```bash
docker run --rm -v "$PWD/config.json":/app/config.json discord-wordle-bot --config /app/config.json
```

The CLI exits with status `0` when it completes normally (including when there is nothing to post), `2` for configuration errors, and `1` for Discord/API runtime failures. It logs the resolved thread date and action taken so cron output is operationally useful.
# discord-wordle-bot
A bot for discord that reminds users to enter their Wordle for the day and other useful chores

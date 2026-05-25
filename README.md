# Discord Wordle Notifier (Go)

A small command-line tool that checks a configured channel for an active daily thread (title like "Apr 25") and posts a reminder into the thread for tracked users who haven't posted a message starting with "Wordle" or "Scoredle".

Quick start

1. Copy `config.sample.json` to `config.json` and fill in `bot_token`, `channel_id`, `starter_prompt`, `tracked_user_ids`, and `timezone` (for example `America/New_York`).
2. Build the program:

```bash
go build -o discord-wordle-bot ./
```

3. Run from cron or manually:

Create today's thread if it doesn't exist:

```bash
./discord-wordle-bot create-thread --config config.json
```

Send reminders for missing users in today's thread:

```bash
./discord-wordle-bot send-reminders --config config.json
```

Scan a specific day's thread history and persist results into SQLite (`wordle_history.db` by default):

```bash
./discord-wordle-bot scan-history --config config.json --date 2026-04-18
./discord-wordle-bot scan-history --config config.json --date 2026-04-18 --db-path /data/wordle_history.db
```

Generate a stats report from stored history. By default `report-stats` writes to stdout and posts to the configured parent Discord channel; use `--output stdout`, `--output discord`, or `--output both` to choose destinations:

```bash
./discord-wordle-bot report-stats --config config.json --period daily --date 2026-04-18
./discord-wordle-bot report-stats --config config.json --period weekly --date 2026-04-18 --output stdout
./discord-wordle-bot report-stats --config config.json --period monthly --date 2026-04-18 --output discord

Manage tracked users (new):

Add a user to the persistent `users` table (creates or updates display name). Flags: `--id` (Discord user ID), `--name` (display name), and optional `--db-path` to point at a different SQLite file.

```bash
./discord-wordle-bot add-user --id 155088535215407106 --name "Alice"
./discord-wordle-bot add-user --id 408856373133180928 --name "Bob" --db-path /data/wordle_history.db
```

When running the other commands (`send-reminders`, `scan-history`, `report-stats`), the program now loads the list of tracked users from the `users` table in the configured (or `--db-path`) SQLite database. If the table is empty the CLI will fall back to the `tracked_user_ids` list in `config.json` for compatibility.
```

**Note** about `--date`: the `--date` value (required, format `YYYY-MM-DD`) selects the reference date for the report and is interpreted in the `timezone` configured in `config.json`.
- `--period daily`: report covers the single calendar day specified by `--date`.
- `--period weekly`: report covers the week containing `--date`, starting on Sunday and ending the following Sunday (start inclusive, end exclusive).
- `--period monthly`: report covers the calendar month containing `--date` (from the 1st to the 1st of the next month).
- `--period yearly`: report covers the calendar year containing `--date`.

The report title shows the provided `--date` as the reference date for the period.

Show help and usage:

```bash
./discord-wordle-bot help
```

4. (Optional) Build and run with Docker

Build the image:

```bash
docker build -t discord-wordle-bot .
```

Run the container (mount your local `config.json` over the container config) and pass a subcommand:

```bash
docker run --rm -v "$PWD/config.json":/app/config.json discord-wordle-bot create-thread --config /app/config.json
docker run --rm -v "$PWD/config.json":/app/config.json discord-wordle-bot send-reminders --config /app/config.json
docker run --rm -v "$PWD/config.json":/app/config.json discord-wordle-bot scan-history --config /app/config.json --date 2026-04-18
docker run --rm -v "$PWD/config.json":/app/config.json discord-wordle-bot report-stats --config /app/config.json --period daily --date 2026-04-18
```

The CLI exits with status `0` when it completes normally (including when there is nothing to post), `2` for configuration errors, and `1` for Discord/API runtime failures. It logs the resolved thread date and action taken so cron output is operationally useful.

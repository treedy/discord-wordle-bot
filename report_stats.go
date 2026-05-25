package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	reportPeriodDaily   = "daily"
	reportPeriodWeekly  = "weekly"
	reportPeriodMonthly = "monthly"
	reportPeriodYearly  = "yearly"
	reportOutputStdout  = "stdout"
	reportOutputDiscord = "discord"
	reportOutputBoth    = "both"
)

var supportedReportPeriods = []string{
	reportPeriodDaily,
	reportPeriodWeekly,
	reportPeriodMonthly,
	reportPeriodYearly,
}

var supportedReportOutputs = []string{
	reportOutputStdout,
	reportOutputDiscord,
	reportOutputBoth,
}

type userPeriodStats struct {
	UserID        string
	Best          *int
	Worst         *int
	Avg           *float64
	Misses        int
	AdjustedAvg   *float64
	DaysReminded  int
	reportedDays  int
	solvedEntries int
}

func runReportStats(cfgPath, periodValue, dateValue, dbPath, outputValue string, stdout, stderr io.Writer) int {
	errorLogger := log.New(stderr, "", log.LstdFlags)

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		errorLogger.Printf("configuration error: %v", err)
		return exitConfigError
	}

	period, err := parseReportPeriod(periodValue)
	if err != nil {
		errorLogger.Printf("configuration error: %v", err)
		return exitConfigError
	}

	targetDate, err := parseScanDate(dateValue, cfg.Location)
	if err != nil {
		errorLogger.Printf("configuration error: %v", err)
		return exitConfigError
	}

	outputMode, err := parseReportOutput(outputValue)
	if err != nil {
		errorLogger.Printf("configuration error: %v", err)
		return exitConfigError
	}

	startDate, endDate, err := reportPeriodBounds(period, targetDate)
	if err != nil {
		errorLogger.Printf("configuration error: %v", err)
		return exitConfigError
	}

	store, err := openHistoryStore(dbPath)
	if err != nil {
		errorLogger.Printf("failed to open history store: %v", err)
		return exitRuntimeError
	}
	defer store.Close()
	submissions, reminders, err := store.queryPeriodStats(context.Background(), startDate, endDate)
	if err != nil {
		errorLogger.Printf("failed to query report stats: %v", err)
		return exitRuntimeError
	}

	// load tracked users from DB (fall back to config if empty)
	users, err := store.listUsers(context.Background())
	if err != nil {
		errorLogger.Printf("failed to load users from DB: %v", err)
		return exitRuntimeError
	}
	trackedIDs := make([]string, 0, len(users))
	displayNames := make(map[string]string, len(users))
	for _, u := range users {
		trackedIDs = append(trackedIDs, u.UserID)
		displayNames[u.UserID] = u.DisplayName
	}
	if len(trackedIDs) == 0 {
		trackedIDs = cfg.TrackedUserIDs
	}

	stats := computeUserStats(trackedIDs, submissions, reminders)
	reportText := formatStatsTable(reportTitle(period, targetDate, cfg.Timezone), stats, displayNames)

	writeStdout := outputMode == reportOutputStdout || outputMode == reportOutputBoth
	postDiscord := outputMode == reportOutputDiscord || outputMode == reportOutputBoth

	if writeStdout {
		if _, err := io.WriteString(stdout, reportText); err != nil {
			errorLogger.Printf("failed to write report output: %v", err)
			return exitRuntimeError
		}
	}

	if postDiscord {
		dg, err := newDiscordSession(cfg.BotToken)
		if err != nil {
			errorLogger.Printf("failed to create discord session: %v", err)
			return exitRuntimeError
		}
		if _, err := sendChannelMessageFn(dg, cfg.ChannelID, reportText); err != nil {
			errorLogger.Printf("failed to post report stats: %v", err)
			return exitRuntimeError
		}
	}

	return exitSuccess
}

func parseReportPeriod(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("--period is required")
	}
	switch value {
	case reportPeriodDaily, reportPeriodWeekly, reportPeriodMonthly, reportPeriodYearly:
		return value, nil
	default:
		return "", fmt.Errorf("invalid --period %q: supported values: %s", value, strings.Join(supportedReportPeriods, ", "))
	}
}

func parseReportOutput(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return reportOutputBoth, nil
	}
	switch value {
	case reportOutputStdout, reportOutputDiscord, reportOutputBoth:
		return value, nil
	default:
		return "", fmt.Errorf("invalid --output %q: supported values: %s", value, strings.Join(supportedReportOutputs, ", "))
	}
}

func reportPeriodBounds(period string, targetDate time.Time) (time.Time, time.Time, error) {
	targetDate = time.Date(targetDate.Year(), targetDate.Month(), targetDate.Day(), 0, 0, 0, 0, targetDate.Location())

	switch period {
	case reportPeriodDaily:
		return targetDate, targetDate.AddDate(0, 0, 1), nil
	case reportPeriodWeekly:
		startDate := targetDate.AddDate(0, 0, -int(targetDate.Weekday()))
		return startDate, startDate.AddDate(0, 0, 7), nil
	case reportPeriodMonthly:
		startDate := time.Date(targetDate.Year(), targetDate.Month(), 1, 0, 0, 0, 0, targetDate.Location())
		return startDate, startDate.AddDate(0, 1, 0), nil
	case reportPeriodYearly:
		startDate := time.Date(targetDate.Year(), time.January, 1, 0, 0, 0, 0, targetDate.Location())
		return startDate, startDate.AddDate(1, 0, 0), nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("invalid --period %q: supported values: %s", period, strings.Join(supportedReportPeriods, ", "))
	}
}

func reportTitle(period string, targetDate time.Time, timezone string) string {
	switch period {
	case reportPeriodDaily:
		return fmt.Sprintf("Daily report for %s (%s)", targetDate.Format(scanDateLayout), timezone)
	case reportPeriodWeekly:
		return fmt.Sprintf("Weekly report for %s (%s)", targetDate.Format(scanDateLayout), timezone)
	case reportPeriodMonthly:
		return fmt.Sprintf("Monthly report for %s (%s)", targetDate.Format(scanDateLayout), timezone)
	case reportPeriodYearly:
		return fmt.Sprintf("Yearly report for %s (%s)", targetDate.Format(scanDateLayout), timezone)
	default:
		return fmt.Sprintf("Report for %s (%s)", targetDate.Format(scanDateLayout), timezone)
	}
}

func computeUserStats(trackedUserIDs []string, submissions []periodSubmissionRow, reminders []periodReminderRow) []userPeriodStats {
	stats := make([]userPeriodStats, 0, len(trackedUserIDs))
	byUser := make(map[string]*userPeriodStats, len(trackedUserIDs))
	for _, userID := range trackedUserIDs {
		stats = append(stats, userPeriodStats{UserID: userID})
		byUser[userID] = &stats[len(stats)-1]
	}

	remindedAtByDate := make(map[string]*time.Time, len(reminders))
	for _, reminder := range reminders {
		remindedAtByDate[reminder.ThreadDate] = reminder.RemindedAt
	}

	standardTotals := make(map[string]int, len(trackedUserIDs))
	adjustedTotals := make(map[string]int, len(trackedUserIDs))

	for _, submission := range submissions {
		stat := byUser[submission.UserID]
		if stat == nil {
			continue
		}

		stat.reportedDays++
		adjustedTotals[submission.UserID] += adjustedScore(submission.Guesses)

		switch {
		case submission.Guesses == 7:
			stat.Misses++
		case submission.Guesses >= 1 && submission.Guesses <= 6:
			guess := submission.Guesses
			if stat.Best == nil || guess < *stat.Best {
				best := guess
				stat.Best = &best
			}
			if stat.Worst == nil || guess > *stat.Worst {
				worst := guess
				stat.Worst = &worst
			}
			stat.solvedEntries++
			standardTotals[submission.UserID] += guess
		}

		if submission.SubmittedAt != nil {
			if remindedAt := remindedAtByDate[submission.ThreadDate]; remindedAt != nil && submission.SubmittedAt.After(*remindedAt) {
				stat.DaysReminded++
			}
		}
	}

	for i := range stats {
		stat := &stats[i]
		if stat.solvedEntries > 0 {
			avg := float64(standardTotals[stat.UserID]) / float64(stat.solvedEntries)
			stat.Avg = &avg
		}
		if stat.reportedDays > 0 {
			adjustedAvg := float64(adjustedTotals[stat.UserID]) / float64(stat.reportedDays)
			stat.AdjustedAvg = &adjustedAvg
		}
	}

	return stats
}

func adjustedScore(guesses int) int {
	switch {
	case guesses >= 1 && guesses <= 6:
		return guesses
	case guesses == 7 || guesses == -1:
		return 7
	default:
		return 0
	}
}

func formatStatsTable(title string, stats []userPeriodStats, displayNames map[string]string) string {
	headers := []string{"User", "Best", "Worst", "Avg", "Misses", "Adj. Avg", "Days Reminded"}

	// build rows
	rows := make([][]string, 0, len(stats))
	for _, stat := range stats {
		userDisplay := stat.UserID
		if dn, ok := displayNames[stat.UserID]; ok && dn != "" {
			userDisplay = dn
		}
		rows = append(rows, []string{
			userDisplay,
			formatOptionalInt(stat.Best),
			formatOptionalInt(stat.Worst),
			formatOptionalFloat(stat.Avg),
			fmt.Sprintf("%d", stat.Misses),
			formatOptionalFloat(stat.AdjustedAvg),
			fmt.Sprintf("%d", stat.DaysReminded),
		})
	}

	// compute column widths (rune-aware)
	colWidths := make([]int, len(headers))
	for i, h := range headers {
		colWidths[i] = utf8.RuneCountInString(h)
	}
	for _, row := range rows {
		for j, cell := range row {
			if w := utf8.RuneCountInString(cell); w > colWidths[j] {
				colWidths[j] = w
			}
		}
	}

	var b strings.Builder
	b.WriteString("*")
	b.WriteString(title)
	b.WriteString("*\n```\n")

	// helper to write separator line
	writeSep := func() {
		b.WriteString("+")
		for _, w := range colWidths {
			b.WriteString(strings.Repeat("-", w+2))
			b.WriteString("+")
		}
		b.WriteString("\n")
	}

	// top separator
	writeSep()

	// header
	b.WriteString("|")
	for i, h := range headers {
		b.WriteString(" ")
		b.WriteString(h)
		b.WriteString(strings.Repeat(" ", colWidths[i]-utf8.RuneCountInString(h)))
		b.WriteString(" |")
	}
	b.WriteString("\n")

	// header separator
	writeSep()

	// rows
	for _, row := range rows {
		b.WriteString("|")
		for j, cell := range row {
			b.WriteString(" ")
			if j == 0 {
				// left align user column
				b.WriteString(cell)
				b.WriteString(strings.Repeat(" ", colWidths[j]-utf8.RuneCountInString(cell)))
			} else {
				// right align numeric columns
				b.WriteString(strings.Repeat(" ", colWidths[j]-utf8.RuneCountInString(cell)))
				b.WriteString(cell)
			}
			b.WriteString(" |")
		}
		b.WriteString("\n")
	}

	// bottom separator
	writeSep()
	b.WriteString("```\n")

	return b.String()
}

func formatOptionalInt(value *int) string {
	if value == nil {
		return "—"
	}
	return fmt.Sprintf("%d", *value)
}

func formatOptionalFloat(value *float64) string {
	if value == nil {
		return "—"
	}
	return fmt.Sprintf("%.2f", *value)
}

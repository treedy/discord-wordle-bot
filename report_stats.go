package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	reportPeriodDaily  = "daily"
	reportOutputStdout = "stdout"
)

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

	stats := computeUserStats(cfg.TrackedUserIDs, submissions, reminders)
	reportText := formatStatsTable(fmt.Sprintf("Daily report for %s (%s)", targetDate.Format(scanDateLayout), cfg.Timezone), stats)

	switch outputMode {
	case reportOutputStdout:
		if _, err := io.WriteString(stdout, reportText); err != nil {
			errorLogger.Printf("failed to write report output: %v", err)
			return exitRuntimeError
		}
	default:
		errorLogger.Printf("configuration error: unsupported report output %q", outputMode)
		return exitConfigError
	}

	return exitSuccess
}

func parseReportPeriod(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("--period is required")
	}
	if value != reportPeriodDaily {
		return "", fmt.Errorf("invalid --period %q: supported values: daily", value)
	}
	return value, nil
}

func parseReportOutput(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return reportOutputStdout, nil
	}
	if value != reportOutputStdout {
		return "", fmt.Errorf("invalid --output %q: supported values: stdout", value)
	}
	return value, nil
}

func reportPeriodBounds(period string, targetDate time.Time) (time.Time, time.Time, error) {
	switch period {
	case reportPeriodDaily:
		return targetDate, targetDate.AddDate(0, 0, 1), nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("invalid --period %q: supported values: daily", period)
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

func formatStatsTable(title string, stats []userPeriodStats) string {
	var builder strings.Builder
	builder.WriteString(title)
	builder.WriteString("\n")

	writer := tabwriter.NewWriter(&builder, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "User\tBest\tWorst\tAvg\tMisses\tAdj. Avg\tDays Reminded")
	for _, stat := range stats {
		fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%d\t%s\t%d\n",
			stat.UserID,
			formatOptionalInt(stat.Best),
			formatOptionalInt(stat.Worst),
			formatOptionalFloat(stat.Avg),
			stat.Misses,
			formatOptionalFloat(stat.AdjustedAvg),
			stat.DaysReminded,
		)
	}
	_ = writer.Flush()

	return builder.String()
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

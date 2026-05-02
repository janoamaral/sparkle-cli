package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/logico/sparkle-cli/internal/config"
	"github.com/logico/sparkle-cli/internal/profiler"
	"github.com/logico/sparkle-cli/internal/tui"
)

func main() {
	if len(os.Args) > 1 && strings.EqualFold(strings.TrimSpace(os.Args[1]), "stats") {
		runStats(os.Args[2:])
		return
	}

	var configPath string
	var initialContext string
	var resultFile string
	var profileEnabled bool

	flag.StringVar(&configPath, "config", "", "override config file path")
	flag.StringVar(&initialContext, "context", "", "seed the input with shell buffer content")
	flag.StringVar(&resultFile, "result-file", "", "write accepted output to this file instead of stdout")
	flag.BoolVar(&profileEnabled, "profile", false, "enable runtime profiling and metrics persistence")
	flag.Parse()

	cfg, loadedConfigPath, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}
	if profileEnabled {
		cfg.Profiler = true
	}

	tracker, err := profiler.New(loadedConfigPath, cfg.Profiler)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	defer func() { _ = tracker.Close() }()

	output, exitCode, err := tui.Run(cfg, loadedConfigPath, initialContext, tracker)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	if tracker.Enabled() {
		printCurrentRunSummary(os.Stderr, tracker, "search")
	}

	if exitCode == 0 && output != "" {
		if err := emitOutput(output, resultFile); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
	}

	os.Exit(exitCode)
}

func runStats(args []string) {
	statsFlags := flag.NewFlagSet("stats", flag.ContinueOnError)
	statsFlags.SetOutput(os.Stderr)
	var configPath string
	var command string
	var last int
	statsFlags.StringVar(&configPath, "config", "", "override config file path")
	statsFlags.StringVar(&command, "command", "search", "command to inspect")
	statsFlags.IntVar(&last, "last", 10, "number of historical runs to compare")
	if err := statsFlags.Parse(args); err != nil {
		os.Exit(2)
	}

	_, loadedConfigPath, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}

	tracker, err := profiler.New(loadedConfigPath, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	defer func() { _ = tracker.Close() }()

	report, err := tracker.Comparison(strings.TrimSpace(command), last)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}
	printComparisonReport(os.Stdout, report, last)
}

func printCurrentRunSummary(output *os.File, tracker profiler.Tracker, command string) {
	rows := tracker.CurrentRun(command)
	if len(rows) == 0 {
		return
	}
	fmt.Fprintln(output, "\n=== Profiling Summary ===")
	fmt.Fprintf(output, "run_id: %s\n", tracker.RunID())
	fmt.Fprintf(output, "command: %s\n", command)
	totalDuration := int64(0)
	for _, row := range rows {
		totalDuration += row.DurationMS
		fmt.Fprintf(output, "- %s: %dms", row.StepName, row.DurationMS)
		if row.TokensOut > 0 {
			fmt.Fprintf(output, " | in=%d out=%d tps=%.2f", row.TokensIn, row.TokensOut, row.TPS)
		}
		fmt.Fprintln(output)
	}
	fmt.Fprintf(output, "total: %dms\n", totalDuration)
}

func printComparisonReport(output *os.File, report profiler.ComparisonReport, last int) {
	if len(report.Steps) == 0 {
		fmt.Fprintln(output, "No profiling data found.")
		return
	}
	sort.Slice(report.Steps, func(i, j int) bool {
		return report.Steps[i].StepName < report.Steps[j].StepName
	})
	fmt.Fprintf(output, "Profiling stats for command=%s\n", report.Command)
	fmt.Fprintf(output, "Current run: %s\n", report.CurrentRun)
	fmt.Fprintf(output, "Compared against: last %d runs\n", last)
	fmt.Fprintln(output, "")

	tw := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "step\tcurrent_ms\tavg_ms(last)\tcurrent_out\tavg_out(last)\tcurrent_tps\tavg_tps(last)")
	for _, step := range report.Steps {
		fmt.Fprintf(tw, "%s\t%d\t%.1f\t%d\t%.1f\t%.2f\t%.2f\n",
			step.StepName,
			step.CurrentDuration,
			step.DurationMS,
			step.CurrentTokensOut,
			step.TokensOut,
			step.CurrentTPS,
			step.TPS,
		)
	}
	_ = tw.Flush()
}

func emitOutput(output, resultFile string) error {
	if strings.TrimSpace(resultFile) == "" {
		fmt.Print(output)
		return nil
	}

	if err := os.WriteFile(resultFile, []byte(output), 0o600); err != nil {
		return fmt.Errorf("write result file %s: %w", resultFile, err)
	}

	return nil
}

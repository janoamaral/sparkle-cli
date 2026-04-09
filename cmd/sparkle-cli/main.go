package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/logico/sparkle-cli/internal/config"
	"github.com/logico/sparkle-cli/internal/tui"
)

func main() {
	var configPath string
	var initialContext string
	var resultFile string

	flag.StringVar(&configPath, "config", "", "override config file path")
	flag.StringVar(&initialContext, "context", "", "seed the input with shell buffer content")
	flag.StringVar(&resultFile, "result-file", "", "write accepted output to this file instead of stdout")
	flag.Parse()

	cfg, _, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}

	output, exitCode, err := tui.Run(cfg, initialContext)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	}

	if exitCode == 0 && output != "" {
		if err := emitOutput(output, resultFile); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(3)
		}
	}

	os.Exit(exitCode)
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

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/jentfoo/SignalSentinel/internal/store"
)

type cliFlags struct {
	ConfigPath     string
	ScannerIP      string
	RecordingsPath string
	ShowHelp       bool
}

func parseFlags(args []string, output io.Writer) (cliFlags, error) {
	if output == nil {
		output = io.Discard
	}
	fs := flag.NewFlagSet("sigsentinel", flag.ContinueOnError)
	fs.SetOutput(output)

	var opts cliFlags
	fs.StringVar(&opts.ConfigPath, "config", "", "path to config YAML")
	fs.StringVar(&opts.ScannerIP, "scanner-ip", "", "scanner IP to persist into config before startup")
	fs.StringVar(&opts.RecordingsPath, "recordings-path", "", "recordings path to persist into config before startup")

	fs.Usage = func() {
		_, _ = fmt.Fprintln(output, "Usage: sigsentinel [flags]")
		_, _ = fmt.Fprintln(output)
		_, _ = fmt.Fprintln(output, "Flags:")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			opts.ShowHelp = true
			return opts, nil
		}
		return cliFlags{}, err
	}
	if fs.NArg() > 0 {
		return cliFlags{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}

	opts.ScannerIP = strings.TrimSpace(opts.ScannerIP)
	opts.RecordingsPath = strings.TrimSpace(opts.RecordingsPath)
	return opts, nil
}

func persistCLIOverrides(opts cliFlags) error {
	if opts.ScannerIP == "" && opts.RecordingsPath == "" {
		return nil
	}

	s := store.New(opts.ConfigPath)
	doc, err := s.Load()
	if err != nil {
		return fmt.Errorf("load config for flags: %w", err)
	}
	if opts.ScannerIP != "" {
		doc.Config.Scanner.IP = opts.ScannerIP
	}
	if opts.RecordingsPath != "" {
		doc.Config.Storage.RecordingsPath = opts.RecordingsPath
	}
	doc.ApplyDefaults()
	if err := doc.Validate(); err != nil {
		return fmt.Errorf("validate config for flags: %w", err)
	}
	if err := s.Save(doc); err != nil {
		return fmt.Errorf("save config for flags: %w", err)
	}
	return nil
}

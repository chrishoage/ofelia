package main

import (
	"fmt"
	"os"

	"github.com/jessevdk/go-flags"
	"github.com/netresearch/ofelia/cli"
	"github.com/netresearch/ofelia/core"
	"github.com/op/go-logging"
)

var version string
var build string

const logFormat = "%{time} %{color} %{shortfile} ▶ %{level} %{color:reset} %{message}"

func buildLogger() core.Logger {
	stdout := logging.NewLogBackend(os.Stdout, "", 0)
	// Set the backends to be used.
	logging.SetBackend(stdout)
	logging.SetFormatter(logging.MustStringFormatter(logFormat))
	// If the OFELIA_LOG_LEVEL environment variable is set, use that as the default log level
	if logLevel, ok := os.LookupEnv("OFELIA_LOG_LEVEL"); ok {
		level, err := logging.LogLevel(logLevel)
		if err != nil {
			fmt.Printf("Invalid log level %s\n", logLevel)
			os.Exit(1)
		}
		logging.SetLevel(level, "ofelia")
	}

	return logging.MustGetLogger("ofelia")
}

func main() {
	logger := buildLogger()
	parser := flags.NewNamedParser("ofelia", flags.Default)
	parser.AddCommand("daemon", "daemon process", "", &cli.DaemonCommand{Logger: logger})
	parser.AddCommand("validate", "validates the config file", "", &cli.ValidateCommand{Logger: logger})
	parser.AddCommand("trigger", "triggers the job and exits", "", &cli.TriggerCommand{Logger: logger})

	if _, err := parser.Parse(); err != nil {
		if flagErr, ok := err.(*flags.Error); ok {
			if flagErr.Type == flags.ErrHelp {
				return
			}

			parser.WriteHelp(os.Stdout)
			fmt.Printf("\nBuild information\n  commit: %s\n  date:%s\n", version, build)
		}

		os.Exit(1)
	}
}

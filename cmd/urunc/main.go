// Copyright (c) 2023-2025, Nubificus LTD
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"errors"
	"io"
	"log/syslog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/sirupsen/logrus"
	lSyslog "github.com/sirupsen/logrus/hooks/syslog"
	m "github.com/urunc-dev/urunc/internal/metrics"
	"github.com/urunc-dev/urunc/pkg/unikontainers"

	_ "github.com/opencontainers/runc/libcontainer/nsenter"
	"github.com/urfave/cli"
)

const (
	specConfig = "config.json"
	usage      = `Open Container Initiative runtime

urunc is a command line client for running unikernel applications packaged according to
the Open Container Initiative (OCI) format and is a compliant implementation of the
Open Container Initiative specification.

Unikernel images are configured using bundles. A bundle for a unikernel is a directory
that includes a specification file named "` + specConfig + `" and a root filesystem.
The root filesystem contains the unikernel and any additional files required to run.

To start a new instance of a unikernel:

    # urunc run [ -b bundle ] <unikernel-id>

Where "<unikernel-id>" is your name for the instance of the unikernel that you
are starting. The name you provide for the unikernel instance must be unique on
your host. Providing the bundle directory using "-b" is optional. The default
value for "bundle" is the current directory.`
)

var version string
var uruncConfig *unikontainers.UruncConfig

// FIXME: We need to find a way to set the output file
// var metrics = m.NewZerologMetrics(constants.TimestampTargetFile)
var metrics m.Writer

// init is used to load the urunc configuration
// if urunc configuration file is not found, it will create a default one
// It also initializes the metrics writer
func init() {
	cfgPath := unikontainers.UruncConfigPath
	var cfg *unikontainers.UruncConfig

	// If config file does not exist, create a default one and save it
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		logrus.Warnf("Urunc config file %s does not exist, creating a default one", cfgPath)
		cfg = unikontainers.DefaultUruncConfig()
		f, err := os.OpenFile(cfgPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			logrus.Warnf("Failed to create urunc config file %s: %v", cfgPath, err)
			uruncConfig = cfg
			return
		}
		defer f.Close()

		encoder := toml.NewEncoder(f)
		if err := encoder.Encode(cfg); err != nil {
			logrus.Warnf("Failed to encode urunc config file %s: %v", cfgPath, err)
			uruncConfig = cfg
			return
		}
	} else {
		// Config file exists, decode it
		cfg = &unikontainers.UruncConfig{}
		if _, err := toml.DecodeFile(cfgPath, cfg); err != nil {
			logrus.Errorf("Failed to decode urunc config file %s: %v", cfgPath, err)
			cfg = unikontainers.DefaultUruncConfig()
		}
	}

	uruncConfig = cfg
	metrics = m.NewZerologMetrics(uruncConfig.Timestamps.Enabled, uruncConfig.Timestamps.Destination)
}

func main() {
	root := "/run/urunc"
	app := cli.NewApp()
	app.Name = "urunc"
	app.Usage = usage
	app.Version = version
	app.Flags = []cli.Flag{
		cli.BoolFlag{
			Name:  "debug",
			Usage: "enable debug logging",
		},
		cli.StringFlag{
			Name:  "log",
			Value: "",
			Usage: "set the log file to write runc logs to (default is '/dev/stderr')",
		},
		cli.StringFlag{
			Name:  "log-format",
			Value: "text",
			Usage: "set the log format ('text' (default), or 'json')",
		},
		cli.StringFlag{
			Name:  "root",
			Value: root,
			Usage: "root directory for storage of container state (this should be located in tmpfs)",
		},
		cli.BoolFlag{
			Name:  "systemd-cgroup",
			Usage: "enable systemd cgroup support, expects cgroupsPath to be of form \"slice:prefix:name\" for e.g. \"system.slice:runc:434234\"",
		},
		cli.StringFlag{
			Name:  "rootless",
			Value: "auto",
			Usage: "ignore cgroup permission errors ('true', 'false', or 'auto')",
		},
	}
	app.Commands = []cli.Command{
		createCommand,
		deleteCommand,
		killCommand,
		runCommand,
		// specCommand,
		startCommand,
		// stateCommand,
	}
	app.Before = func(context *cli.Context) error {
		if err := reviseRootDir(context); err != nil {
			return err
		}
		return configLogrus(context, uruncConfig.Log)
	}

	// If the command returns an error, cli takes upon itself to print
	// the error on cli.ErrWriter and exit.
	// Use our own writer here to ensure the log gets sent to the right location.
	cli.ErrWriter = &FatalWriter{cli.ErrWriter}

	if err := app.Run(os.Args); err != nil {
		fatal(err)
	}
}

type FatalWriter struct {
	cliErrWriter io.Writer
}

func (f *FatalWriter) Write(p []byte) (n int, err error) {
	logrus.Error(string(p))
	if !logrusToStderr() {
		return f.cliErrWriter.Write(p)
	}
	return len(p), nil
}

// reviseRootDir ensures that the --root option argument,
// if specified, is converted to an absolute and cleaned path,
// and that this path is sane.
func reviseRootDir(context *cli.Context) error {
	if !context.IsSet("root") {
		return nil
	}
	root, err := filepath.Abs(context.GlobalString("root"))
	if err != nil {
		return err
	}
	if root == "/" {
		// This can happen if --root argument is
		//  - "" (i.e. empty);
		//  - "." (and the CWD is /);
		//  - "../../.." (enough to get to /);
		//  - "/" (the actual /).
		return errors.New("option --root argument should not be set to /")
	}

	return context.GlobalSet("root", root)
}

func configLogrus(context *cli.Context, cfg unikontainers.UruncLog) error {
	// Determine the highest log level between CLI flag and config (TRACE is 6, PANIC is 0)
	getLogLevel := func(levelStr, fallback string) (logrus.Level, error) {
		if levelStr == "" {
			levelStr = fallback
		}
		level, err := logrus.ParseLevel(levelStr)
		if err == nil {
			return level, nil
		}
		return logrus.ParseLevel(fallback)
	}

	cliLogLevelStr := "info"
	if context.GlobalBool("debug") {
		cliLogLevelStr = "debug"
	}
	cliLogLevel, err := getLogLevel(cliLogLevelStr, "info")
	if err != nil {
		return err
	}

	cfgLogLevel, err := getLogLevel(cfg.Level, "info")
	if err != nil {
		return err
	}

	logLevel := cliLogLevel
	if cfgLogLevel > cliLogLevel {
		logLevel = cfgLogLevel
	}
	logrus.SetLevel(logLevel)

	// If loglevel is debug or lower, enable report caller with prettyfier for text format
	if logLevel >= logrus.DebugLevel {
		logrus.SetReportCaller(true)
		_, file, _, _ := runtime.Caller(0)
		prefix := filepath.Dir(file) + "/"
		logrus.SetFormatter(&logrus.TextFormatter{
			CallerPrettyfier: func(f *runtime.Frame) (string, string) {
				function := strings.TrimPrefix(f.Function, prefix) + "()"
				fileLine := strings.TrimPrefix(f.File, prefix) + ":" + strconv.Itoa(f.Line)
				return function, fileLine
			},
		})
	}

	// Syslog hook if enabled
	if cfg.Syslog {
		hook, err := lSyslog.NewSyslogHook("", "", syslog.LOG_DEBUG, "")
		if err != nil {
			return err
		}
		logrus.AddHook(hook)
	}

	// Set log format
	switch f := context.GlobalString("log-format"); f {
	case "", "text":
		// do nothing, already set above if debug
	case "json":
		logrus.SetFormatter(new(logrus.JSONFormatter))
	default:
		return errors.New("invalid log-format: " + f)
	}

	// Log file output if set
	if file := context.GlobalString("log"); file != "" {
		f, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_SYNC, 0o644)
		if err != nil {
			return err
		}
		logrus.SetOutput(f)
	}

	return nil
}

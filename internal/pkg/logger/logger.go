package logger

import (
	"io"
	"os"
	"time"

	"github.com/metall/mcp-web-scrape/internal/pkg/config"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func Init(cfg config.LogConfig) error {
	// Set time format
	zerolog.TimeFieldFormat = time.RFC3339

	// Set log level
	level, err := zerolog.ParseLevel(cfg.Level)
	if err != nil {
		return err
	}
	zerolog.SetGlobalLevel(level)

	// Configure output
	var output io.Writer = os.Stdout
	if cfg.Pretty {
		output = zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: "15:04:05",
		}
	}

	// Set global logger
	log.Logger = zerolog.New(output).With().Timestamp().Logger()

	return nil
}

func Get() zerolog.Logger {
	return log.Logger
}

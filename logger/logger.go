package logger

import (
	"fmt"
	"io"
	"os"
	"time"

	zlogsentry "github.com/archdx/zerolog-sentry"
	"github.com/isrc-cas/gt/logger/file-rotatelogs"
	"github.com/isrc-cas/gt/predef"
	zerolog "github.com/rs/zerolog"
)

// Options represents the options of logger passed to Init
type Options struct {
	FilePath      string
	RotationCount uint
	RotationSize  int64
	Level         string

	SentryDSN         string
	SentryLevels      []string
	SentrySampleRate  float64
	SentryRelease     string
	SentryEnvironment string
	SentryServerName  string
	SentryDebug       bool
}

type syncer interface {
	io.WriteCloser
	Sync() error
}

// Logger is the main logger object
type Logger struct {
	zerolog.Logger
	out    syncer
	sentry io.WriteCloser
}

// Init initializes the global variable Logger.
func Init(options Options) (logger Logger, err error) {
	level, err := zerolog.ParseLevel(options.Level)
	if err != nil {
		return
	}

	var logWriter io.Writer
	var out syncer
	var sentry io.WriteCloser
	if len(options.FilePath) > 0 {
		out, err = rotatelogs.New(
			options.FilePath+".%Y%m%d",
			rotatelogs.WithRotationCount(options.RotationCount),
			rotatelogs.WithRotationSize(options.RotationSize),
			rotatelogs.WithLinkName(options.FilePath),
		)
		if err != nil {
			return
		}
		logWriter = zerolog.ConsoleWriter{Out: out, TimeFormat: time.UnixDate, NoColor: true}
	} else {
		logWriter = zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.UnixDate}
	}
	if len(options.SentryDSN) > 0 {
		var opts []zlogsentry.WriterOption
		if len(options.SentryLevels) > 0 {
			levels := make([]zerolog.Level, len(options.SentryLevels))
			for i, l := range options.SentryLevels {
				level, err = zerolog.ParseLevel(l)
				if err != nil {
					return
				}
				switch level {
				case zerolog.Disabled, zerolog.NoLevel:
					err = fmt.Errorf("invalid -sentryLevel '%s'", l)
					return
				}
				levels[i] = level
			}
			opts = append(opts, zlogsentry.WithLevels(levels...))
		}
		if options.SentrySampleRate >= 0 {
			opts = append(opts, zlogsentry.WithSampleRate(options.SentrySampleRate))
		}
		if len(options.SentryRelease) > 0 {
			opts = append(opts, zlogsentry.WithRelease(options.SentryRelease))
		}
		if len(options.SentryEnvironment) > 0 {
			opts = append(opts, zlogsentry.WithEnvironment(options.SentryEnvironment))
		}
		if len(options.SentryServerName) > 0 {
			opts = append(opts, zlogsentry.WithServerName(options.SentryServerName))
		}
		if options.SentryDebug {
			opts = append(opts, zlogsentry.WithDebug())
		}
		sentry, err = zlogsentry.New(options.SentryDSN, opts...)
		if err != nil {
			return
		}
		logWriter = io.MultiWriter(logWriter, sentry)
	}
	if predef.Debug {
		logger = Logger{
			Logger: zerolog.New(logWriter).With().Caller().Timestamp().Logger().Level(level),
			out:    out,
			sentry: sentry,
		}
	} else {
		logger = Logger{
			Logger: zerolog.New(logWriter).With().Timestamp().Logger().Level(level),
			out:    out,
			sentry: sentry,
		}
	}
	return
}

// Close commits the current contents and close the underlying writer
func (l *Logger) Close() {
	if l.sentry != nil {
		err := l.sentry.Close()
		if err != nil {
			l.Error().Err(err).Msg("failed to close sentry")
		}
	}
	if l.out != nil {
		err := l.out.Sync()
		if err != nil {
			l.Error().Err(err).Msg("failed to sync log file")
		}
		err = l.out.Close()
		if err != nil {
			l.Error().Err(err).Msg("failed to close log file")
		}
	}
}

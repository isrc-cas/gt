package client

import (
	"github.com/isrc-cas/gt/config"
	"github.com/isrc-cas/gt/predef"
	"github.com/rs/zerolog"
	"time"
)

// Config is a client config.
type Config struct {
	Version string // 目前未使用
	Options
}

// Options is the config options for a client.
type Options struct {
	Config             string        `arg:"config" yaml:"-" usage:"The config file path to load"`
	ID                 string        `yaml:"id" usage:"The unique id used to connect to server. Now it's the prefix of the domain."`
	Secret             string        `yaml:"secret" usage:"The secret used to verify the id"`
	ReconnectDelay     time.Duration `yaml:"reconnectDelay" usage:"The delay before reconnect. Supports values like '30s', '5m'"`
	Remote             string        `yaml:"remote" usage:"The remote server url. Support tcp:// and tls://, default tcp://"`
	RemoteAPI          string        `yaml:"remoteAPI" usage:"The API to get remote server url"`
	RemoteCert         string        `yaml:"remoteCert" usage:"The path to remote cert"`
	RemoteCertInsecure bool          `yaml:"remoteCertInsecure" usage:"Accept self-signed SSL certs from remote"`
	RemoteConnections  uint          `yaml:"remoteConnections" usage:"The number of connections to server"`
	RemoteTimeout      time.Duration `yaml:"remoteTimeout" usage:"The timeout of remote connections. Supports values like '30s', '5m'"`
	Local              string        `yaml:"local" usage:"The local service url"`
	LocalTimeout       time.Duration `yaml:"localTimeout" usage:"The timeout of local connections. Supports values like '30s', '5m'"`
	UseLocalAsHTTPHost bool          `yaml:"useLocalAsHTTPHost" usage:"Use the local address as host"`

	SentryDSN         string             `yaml:"sentryDSN" usage:"Sentry DSN to use"`
	SentryLevel       config.StringSlice `yaml:"sentryLevel" usage:"Sentry levels: trace, debug, info, warn, error, fatal, panic (default [\"error\", \"fatal\", \"panic\"])"`
	SentrySampleRate  float64            `yaml:"sentrySampleRate" usage:"Sentry sample rate for event submission: [0.0 - 1.0]"`
	SentryRelease     string             `yaml:"sentryRelease" usage:"Sentry release to be sent with events"`
	SentryEnvironment string             `yaml:"sentryEnvironment" usage:"Sentry environment to be sent with events"`
	SentryServerName  string             `yaml:"sentryServerName" usage:"Sentry server name to be reported"`
	SentryDebug       bool               `yaml:"sentryDebug" usage:"Sentry debug mode, the debug information is printed to help you understand what sentry is doing"`

	LogFile         string `yaml:"logFile" usage:"Path to save the log file"`
	LogFileMaxSize  int64  `yaml:"logFileMaxSize" usage:"Max size of the log files"`
	LogFileMaxCount uint   `yaml:"logFileMaxCount" usage:"Max count of the log files"`
	LogLevel        string `yaml:"logLevel" usage:"Log level: trace, debug, info, warn, error, fatal, panic, disable"`
	Version         bool   `arg:"version" yaml:"-" usage:"Show the version of this program"`
}

func defaultConfig() Config {
	return Config{
		Options: Options{
			ReconnectDelay:    5 * time.Second,
			RemoteTimeout:     5 * time.Second,
			RemoteConnections: 1,
			LocalTimeout:      120 * time.Second,
			LogFileMaxCount:   7,
			LogFileMaxSize:    512 * 1024 * 1024,
			LogLevel:          zerolog.InfoLevel.String(),

			SentrySampleRate: 1.0,
			SentryRelease:    predef.Version,
		},
	}
}

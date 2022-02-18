package server

import (
	"errors"
	"fmt"
	"github.com/isrc-cas/gt/config"
	"github.com/isrc-cas/gt/predef"
	"github.com/isrc-cas/gt/server/sync"
	"github.com/rs/zerolog"
	"time"
)

// Config is a server config.
type Config struct {
	Version string          // 目前未使用
	Users   map[string]user `yaml:"users"`
	Options
}

// Options is the config Options for a server.
type Options struct {
	Config        string `arg:"config" yaml:"-" usage:"The config file path to load"`
	Addr          string `yaml:"addr" usage:"The address to listen on. Supports values like: '80', ':80' or '0.0.0.0:80'"`
	TLSAddr       string `yaml:"tlsAddr" usage:"The address for tls to listen on. Supports values like: '443', ':443' or '0.0.0.0:443'"`
	TLSMinVersion string `yaml:"tlsVersion" usage:"The tls min version, supported values: tls1.1, tls1.2, tls1.3"`
	CertFile      string `yaml:"certFile" usage:"The path to cert file"`
	KeyFile       string `yaml:"keyFile" usage:"The path to key file"`

	ID             config.StringSlice `arg:"id" yaml:"-" usage:"The user id"`
	Secret         config.StringSlice `arg:"secret" yaml:"-" usage:"The secret for user id"`
	Users          string             `yaml:"users" usage:"The users yaml file to load"`
	AuthAPI        string             `yaml:"authAPI" usage:"The API to authenticate user with id and secret"`
	AllowAnyClient bool               `yaml:"allowAnyClient" usage:"Allow any client to connect to the server"`

	HTTPMUXHeader string `yaml:"httpMUXHeader" usage:"The http multiplexing header to be used"`

	Timeout                        time.Duration `yaml:"timeout" usage:"The timeout of connections. Supports values like '30s', '5m'"`
	TimeoutOnUnidirectionalTraffic bool          `yaml:"timeoutOnUnidirectionalTraffic" usage:"Timeout will happens when traffic is unidirectional"`

	// internal api service
	APIAddr          string `yaml:"apiAddr" usage:"The address to listen on for internal api service. Supports values like: '8080', ':8080' or '0.0.0.0:8080'"`
	APICertFile      string `yaml:"apiCertFile" usage:"The path to cert file"`
	APIKeyFile       string `yaml:"apiKeyFile" usage:"The path to key file"`
	APITLSMinVersion string `yaml:"apiTLSVersion" usage:"The tls min version, supported values: tls1.1, tls1.2, tls1.3"`

	// TURN service
	TURNAddr           string        `yaml:"turnAddr" usage:"The address to listen on for TURN service. Supports values like: '3478', ':3478' or '0.0.0.0:3478'"`
	ChannelBindTimeout time.Duration `yaml:"channelBindTimeout" usage:"The timeout of channel binding. Supports values like '30s', '5m'"`

	SNIAddr string `yaml:"sniAddr" usage:"The address to listen on for raw tls proxy. Host comes from Server Name Indication. Supports values like: '443', ':443' or '0.0.0.0:443'"`

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
			Addr:             "80",
			Timeout:          90 * time.Second,
			TLSMinVersion:    "tls1.2",
			APITLSMinVersion: "tls1.2",
			LogFileMaxCount:  7,
			LogFileMaxSize:   512 * 1024 * 1024,
			LogLevel:         zerolog.InfoLevel.String(),

			SentrySampleRate: 1.0,
			SentryRelease:    predef.Version,

			HTTPMUXHeader: "Host",

			TURNAddr:           "3478",
			ChannelBindTimeout: 2 * time.Minute,
		},
	}
}

// user 用户权限细节
type user struct {
	Secret string
	temp   bool
}

// users 客户端的权限管理
type users struct {
	sync.Map
}

// 合并 users 配置文件和命令行的 users
func (u *users) mergeUsers(users map[string]user, id, secret []string) error {
	for id, ud := range users {
		u.Store(id, ud)
	}

	if len(id) != len(secret) {
		return errors.New("the number of id does not match the number of secret")
	}
	for i := 0; i < len(id); i++ {
		u.Store(id[i], user{
			Secret: secret[i],
		})
	}

	return u.verify()
}

func (u *users) verify() (err error) {
	u.Range(func(idValue, userValue interface{}) bool {
		id := idValue.(string)
		user := userValue.(user)
		if len(id) < predef.MinIDSize || len(id) > predef.MaxIDSize {
			err = fmt.Errorf("invalid id length: '%s'", id)
		}

		if len(user.Secret) < predef.MinSecretSize || len(user.Secret) > predef.MaxSecretSize {
			err = fmt.Errorf("invalid secret length: '%s'", user.Secret)
		}
		return true
	})
	return
}

func (u *users) empty() (empty bool) {
	empty = true
	u.Range(func(key, value interface{}) bool {
		empty = false
		return false
	})
	return
}

func (u *users) auth(id string, secret string) (ok bool) {
	value, ok := u.Load(id)
	if !ok {
		return
	}
	if ud, ok := value.(user); ok && ud.Secret == secret {
		return true
	}
	return
}

func (u *users) idConflict(id string) bool {
	_, ok := u.Load(id)
	return ok
}

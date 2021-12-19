package server

import (
	"errors"
	"fmt"
	"time"

	"github.com/isrc-cas/gt/config"
	"github.com/isrc-cas/gt/predef"
	"github.com/rs/zerolog"
)

// Config is a server config.
type Config struct {
	Version string // 目前未使用
	Users   Users  `yaml:"users"`
	Options
}

// Options is the config Options for a server.
type Options struct {
	Config        string `arg:"config" yaml:"-" usage:"The config file path to load"`
	Addr          string `yaml:"addr" usage:"The address to listen on. Bare port is supported"`
	TLSAddr       string `yaml:"tlsAddr" usage:"The address for tls to listen on. Bare port is supported"`
	TLSMinVersion string `yaml:"tlsVersion" usage:"The tls min version, supported values: tls1.1, tls1.2, tls1.3"`
	CertFile      string `yaml:"certFile" usage:"The path to cert file"`
	KeyFile       string `yaml:"keyFile" usage:"The path to key file"`

	// 只用于显示帮助信息，解析结果在 Config.Users
	ID      config.StringSlice `arg:"id" yaml:"-" usage:"The user id"`
	Secret  config.StringSlice `arg:"secret" yaml:"-" usage:"The secret for user id"`
	Users   string             `yaml:"users" usage:"The users yaml file to load"`
	AuthAPI string             `yaml:"authAPI" usage:"The API to authenticate with id and secret"`

	HTTPMUXHeader string `yaml:"httpMUXHeader" usage:"The http multiplexing header to be used"`

	Timeout time.Duration `yaml:"timeout" usage:"timeout of connections"`

	// internal api service
	APIAddr string `yaml:"apiAddr" usage:"The address to listen on for internal api service. Bare port is supported"`

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
	config := Config{
		Users: make(Users),
		Options: Options{
			Addr:            "80",
			Timeout:         90 * time.Second,
			TLSMinVersion:   "tls1.2",
			LogFileMaxCount: 7,
			LogFileMaxSize:  512 * 1024 * 1024,
			LogLevel:        zerolog.InfoLevel.String(),

			SentrySampleRate: 1.0,

			HTTPMUXHeader: "Host",
		},
	}

	return config
}

// UserDetail 用户权限细节
type UserDetail struct {
	Secret string
}

// Users 客户端的权限管理
type Users map[string]UserDetail

// 合并 users 配置文件和命令行的 users
func (u Users) mergeUsers(usersYaml Users, id, secret []string) error {
	for id, ud := range usersYaml {
		u[id] = ud
	}

	if len(id) != len(secret) {
		return errors.New("the number of id does not match the number of secret")
	}
	for i := 0; i < len(id); i++ {
		u[id[i]] = UserDetail{
			Secret: secret[i],
		}
	}

	return nil
}

func (u Users) store(id string, ud UserDetail) {
	u[id] = ud
}

func (u Users) load(id string) (UserDetail, bool) {
	if ud, ok := u[id]; ok {
		return ud, true
	}
	return UserDetail{}, false
}

func (u Users) delete(id string) {
	delete(u, id)
}

func (u Users) empty() bool {
	return len(u) == 0
}

func (u Users) auth(id string, secret string) (ok bool) {
	if ud, ok := u[id]; ok {
		return ud.Secret == secret
	}
	return
}

func (u Users) verify() error {
	for id, user := range u {
		if len(id) < predef.MinIDSize || len(id) > predef.MaxIDSize {
			return fmt.Errorf("id length invalid: '%s'", id)
		}

		if len(user.Secret) < predef.MinSecretSize || len(user.Secret) > predef.MaxSecretSize {
			return fmt.Errorf("secret length invalid: '%s'", user.Secret)
		}
	}

	return nil
}

func (u Users) idConflict(id string) bool {
	_, ok := u[id]
	return ok
}

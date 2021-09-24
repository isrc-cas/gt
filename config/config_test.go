package config

import (
	"github.com/rs/zerolog"
	"reflect"
	"testing"
	"time"
)

type Config struct {
	Version string
	Options
}

type Options struct {
	Config              string        `arg:"config" yaml:"-" usage:"The config file path to load"`
	ID                  string        `yaml:"id" usage:"The unique id used to connect to server"`
	Server              string        `yaml:"server" usage:"The server url"`
	ServerCert          string        `yaml:"serverCert" usage:"The cert path of server"`
	ServerCertInsecure  bool          `yaml:"serverCertInsecure" usage:"Accept self-signed SSL certs from the server"`
	ServerTimeout       time.Duration `yaml:"serverTimeout" usage:"The timeout for server connections"`
	Service             string        `yaml:"service" usage:"The service gateway url"`
	ServiceCert         string        `yaml:"serviceCert" usage:"The cert path of service gateway"`
	ServiceCertInsecure bool          `yaml:"serviceCertInsecure" usage:"Accept self-signed SSL certs from the service gateway"`
	ServiceTimeout      time.Duration `yaml:"serviceTimeout" usage:"The timeout for service gateway connections"`
	LogFile             string        `yaml:"logFile" usage:"Path to save the log file"`
	LogFileMaxSize      int64         `yaml:"logFileMaxSize" usage:"Max size of the log files"`
	LogFileMaxCount     uint          `yaml:"logFileMaxCount" usage:"Max count of the log files"`
	LogLevel            string        `yaml:"logLevel" usage:"Log level: trace, debug, info, warn, error, fatal, panic, disable"`
	Version             bool          `arg:"version" yaml:"-" usage:"Show the version of this program"`
}

func defaultConfig() Config {
	return Config{
		Options: Options{
			ServerTimeout:   120 * time.Second,
			ServiceTimeout:  120 * time.Second,
			LogFileMaxCount: 7,
			LogFileMaxSize:  512 * 1024 * 1024,
			LogLevel:        zerolog.InfoLevel.String(),
		},
	}
}

func TestParseFlags(t *testing.T) {
	type args struct {
		args []string
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
		want    Config
	}{
		{
			"config",
			args{[]string{"net", "-config", "testdata/config.yaml"}},
			false,
			Config{
				Version: "1.0",
				Options: Options{
					Config:              "testdata/config.yaml",
					Server:              "tls://localhost:443",
					ServerCertInsecure:  true,
					ServerTimeout:       60 * time.Second,
					Service:             "https://localhost:8443",
					ServiceCertInsecure: true,
					ServiceTimeout:      180 * time.Second,
					LogFileMaxCount:     7,
					LogFileMaxSize:      512 * 1024 * 1024,
					LogLevel:            zerolog.InfoLevel.String(),
				}},
		},
		{
			"overwrite config",
			args{[]string{"net", "-config", "testdata/config.yaml", "-server", "tls://localhost:9443", "-logFileMaxCount", "8"}},
			false,
			Config{
				Version: "1.0",
				Options: Options{
					Config:              "testdata/config.yaml",
					Server:              "tls://localhost:9443",
					ServerCertInsecure:  true,
					ServerTimeout:       60 * time.Second,
					Service:             "https://localhost:8443",
					ServiceCertInsecure: true,
					ServiceTimeout:      180 * time.Second,
					LogFileMaxCount:     8,
					LogFileMaxSize:      512 * 1024 * 1024,
					LogLevel:            zerolog.InfoLevel.String(),
				}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := defaultConfig()
			if err := ParseFlags(tt.args.args, &config, &config.Options); (err != nil) != tt.wantErr {
				t.Errorf("ParseFlags() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(&tt.want, &config) {
				t.Errorf("ParseFlags() got = \n%#v\n, want \n%#v", config, tt.want)
			}
		})
	}
}

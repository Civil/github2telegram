package configs

import (
	"database/sql"
	"regexp"
	"sync"
	"time"

	"github.com/lomik/zapwriter"
)

type FiltersConfig struct {
	Name           string
	Filter         string
	MessagePattern string

	FilterRegex     *regexp.Regexp
	FilterProcessed bool
	LastUpdateTime  time.Time
	LastTag         string
}

type NotificationConfig struct {
	Token string
	Url   string
	Type  string
}

type NotificationEndpoints interface {
	Send(url, filter, message string) error
	Process()
}

type FeedsConfig struct {
	Repo    string
	Filters []FiltersConfig

	PollingInterval time.Duration
	Notifications   []string
}

var DefaultLoggerConfig = zapwriter.Config{
	Logger:           "",
	File:             "stdout",
	Level:            "debug",
	Encoding:         "json",
	EncodingTime:     "iso8601",
	EncodingDuration: "seconds",
}

type Configuration struct {
	sync.RWMutex
	Listen           string                        `yaml:"listen"`
	Logger           []zapwriter.Config            `yaml:"logger"`
	DatabaseType     string                        `yaml:"database_type"`
	DatabaseURL      string                        `yaml:"database_url"`
	DatabaseLogin    string                        `yaml:"database_login"`
	DatabasePassword string                        `yaml:"database_password"`
	AdminUsername    string                        `yaml:"admin_username"`
	PollingInterval  time.Duration                 `yaml:"polling_interval"`
	Endpoints        map[string]NotificationConfig `yaml:"endpoints"`

	DB              *sql.DB                          `yaml:"-"`
	Senders         map[string]NotificationEndpoints `yaml:"-"`
	FeedsConfig     []*FeedsConfig                   `yaml:"-"`
	CurrentId       int                              `yaml:"-"`
	ProcessingFeeds map[string]bool                  `yaml:"-"`
}

var Config = Configuration{
	AdminUsername:   "REPLACE_ME",
	Listen:          "127.0.0.1:8080",
	Logger:          []zapwriter.Config{DefaultLoggerConfig},
	DatabaseType:    "sqlite3",
	DatabaseURL:     "./github2telegram.DB",
	PollingInterval: 5 * time.Minute,
	ProcessingFeeds: make(map[string]bool),
}

func (c *Configuration) GetDB() *sql.DB {
	return c.DB
}

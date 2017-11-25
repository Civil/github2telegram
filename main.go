package main

import (
	"flag"
	"github.com/lomik/zapwriter"
	"regexp"

	"database/sql"
	"go.uber.org/zap"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"log"

	_ "net/http/pprof"

	"net/http"
)

var DefaultLoggerConfig = zapwriter.Config{
	Logger:           "",
	File:             "stdout",
	Level:            "info",
	Encoding:         "console",
	EncodingTime:     "iso8601",
	EncodingDuration: "seconds",
}

type FiltersConfig struct {
	Name           string
	Filter         string
	MessagePattern string

	filterRegex     *regexp.Regexp
	filterProcessed bool
	lastUpdateTime  time.Time
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

type Feed struct {
	Repo           string
	Filter         string
	Name           string
	MessagePattern string

	filterRegex     *regexp.Regexp
	filterProcessed bool
	lastUpdateTime  time.Time
}

var config = struct {
	sync.RWMutex
	Listen           string
	Logger           []zapwriter.Config
	DatabaseType     string
	DatabaseURL      string
	DatabaseLogin    string
	DatabasePassword string
	AdminUsername    string

	Endpoints       map[string]NotificationConfig
	db              *sql.DB
	wg              sync.WaitGroup
	senders         map[string]NotificationEndpoints
	feedsConfig     []FeedsConfig
	currentId       int
	processingFeeds map[string]bool
}{
	AdminUsername: "Civiloid",
	Listen:        ":8080",
	Endpoints: map[string]NotificationConfig{
		"telegram": {
			Type:  "telegram",
			Token: "CHANGE_ME",
		},
	},
	Logger:          []zapwriter.Config{DefaultLoggerConfig},
	DatabaseType:    "sqlite3",
	DatabaseURL:     "./github2telegram.db",
	processingFeeds: make(map[string]bool),
}

const (
	current_schema_version = 1
)

func initSqlite() {
	var err error
	logger := zapwriter.Logger("main")

	config.db, err = sql.Open("sqlite3", config.DatabaseURL)
	if err != nil {
		logger.Fatal("unable to open database file",
			zap.Any("config", config),
			zap.Error(err),
		)
	}

	rows, err := config.db.Query("SELECT version from 'schema_version' where id=1")
	if err != nil {
		if err.Error() == "no such table: schema_version" {
			_, err = config.db.Exec(`
					CREATE TABLE IF NOT EXISTS 'schema_version' (
						'id' INTEGER PRIMARY KEY AUTOINCREMENT,
						'version' INTEGER NOT NULL
					);

					CREATE TABLE IF NOT EXISTS 'last_version' (
						'id' INTEGER PRIMARY KEY AUTOINCREMENT,
						'url' VARCHAR(255) NOT NULL,
						'filter' VARCHAR(255) NOT NULL,
						'date' DATE NOT NULL
					);

					CREATE TABLE IF NOT EXISTS 'subscriptions' (
						'id' INTEGER PRIMARY KEY AUTOINCREMENT,
						'chat_id' Int64,
						'endpoint' VARCHAR(255) NOT NULL,
						'url' VARCHAR(255) NOT NULL,
						'filter' VARCHAR(255) NOT NULL
					);

					CREATE TABLE IF NOT EXISTS 'feeds' (
						'id' INTEGER PRIMARY KEY AUTOINCREMENT,
						'repo' VARCHAR(255) NOT NULL,
						'filter' VARCHAR(255) NOT NULL,
						'name' VARCHAR(255) NOT NULL,
						'message_pattern' VARCHAR(255) NOT NULL
					);

					INSERT INTO 'schema_version' (id, version) values (1, 1);
				`)
			if err != nil {
				logger.Fatal("failed to initialize database",
					zap.Any("config", config),
					zap.Error(err),
				)
			}
		} else {
			logger.Fatal("failed to query database version",
				zap.Error(err),
			)
		}
	} else {
		schema_version := int(0)
		for rows.Next() {
			err = rows.Scan(&schema_version)
			if err != nil {
				logger.Fatal("unable to fetch value",
					zap.Error(err),
				)
			}
		}
		rows.Close()

		if schema_version != current_schema_version {
			logger.Fatal("Unknown schema version specified",
				zap.Int("version", schema_version),
			)

			/*
				_, err = config.db.Exec("INSERT OR REPLACE into 'schema_version' (id, version) values (1, 1);")
				if err != nil {
					logger.Fatal("failed to update database schema version",
						zap.Any("config", config),
						zap.Error(err),
					)
				}
			*/
		}
	}

}

func updateFeeds(feeds []Feed) {
	config.Lock()
	defer config.Unlock()

	for _, feed := range feeds {
		var cfg *FeedsConfig
		for i := range config.feedsConfig {
			if config.feedsConfig[i].Repo == feed.Repo {
				cfg = &config.feedsConfig[i]
				break
			}
		}
		if cfg == nil {
			cfg := FeedsConfig{
				Repo:            feed.Repo,
				PollingInterval: 30 * time.Minute,
				Filters: []FiltersConfig{{
					Name:           feed.Name,
					Filter:         feed.Filter,
					MessagePattern: feed.MessagePattern,
				}},
			}

			config.feedsConfig = append(config.feedsConfig, cfg)
			continue
		}
		cfg.Filters = append(cfg.Filters, FiltersConfig{
			Name:           feed.Name,
			Filter:         feed.Filter,
			MessagePattern: feed.MessagePattern,
		})
	}

	logger := zapwriter.Logger("haha")
	logger.Info("debug",
		zap.Any("cfg", config.feedsConfig),
	)

	for i := range config.feedsConfig {
		if _, ok := config.processingFeeds[config.feedsConfig[i].Repo]; !ok {
			logger.Info("id",
				zap.Int("id", i),
			)
			config.processingFeeds[config.feedsConfig[i].Repo] = true
			config.wg.Add(1)
			go func(id int) {
				processFeed(id)
				config.wg.Done()
			}(i)
		}
	}
}

func main() {
	err := zapwriter.ApplyConfig([]zapwriter.Config{DefaultLoggerConfig})
	if err != nil {
		log.Fatal("Failed to initialize logger with default configuration")

	}
	logger := zapwriter.Logger("main")

	configFile := flag.String("c", "", "config file (json)")
	flag.Parse()

	if *configFile != "" {
		cfgRaw, err := ioutil.ReadFile(*configFile)
		if err != nil {
			logger.Fatal("unable to load config file:",
				zap.Error(err),
			)
		}

		err = yaml.Unmarshal(cfgRaw, &config)
		if err != nil {
			logger.Fatal("error parsing config file",
				zap.Error(err),
			)
		}

		err = zapwriter.ApplyConfig(config.Logger)
		if err != nil {
			logger.Fatal("failed to apply config",
				zap.Any("config", config.Logger),
				zap.Error(err),
			)
		}
	}

	if config.DatabaseType != "sqlite3" {
		logger.Fatal("unsupported database",
			zap.String("database_type", config.DatabaseType),
			zap.Strings("supported_database_types", []string{"sqlite3"}),
		)
	}

	if config.DatabaseType == "sqlite3" {
		initSqlite()
	}

	config.senders = make(map[string]NotificationEndpoints)

	for name, cfg := range config.Endpoints {
		if cfg.Type == "telegram" {
			config.senders[name] = initializeTelegramEndpoint(cfg.Token)
			go config.senders[name].Process()
		} else {
			logger.Fatal("unknown type",
				zap.String("type", cfg.Type),
				zap.Strings("supported_types", []string{"telegram"}),
			)
		}
	}

	logger.Info("github2telegram initialized",
		zap.Any("config", config),
	)

	feeds, err := listFeeds()
	if err != nil {
		logger.Fatal("unknown error quering database",
			zap.Error(err),
		)
	}

	updateFeeds(feeds)

	config.wg.Wait()
	err = http.ListenAndServe(config.Listen, nil)
	logger.Fatal("error creating http server",
		zap.Error(err),
	)
}

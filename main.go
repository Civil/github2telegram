package main

import (
	"database/sql"
	"flag"
	"go.uber.org/zap"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
	"net/http"
	_ "net/http/pprof"

	"github.com/Civil/github2telegram/configs"
	"github.com/Civil/github2telegram/db"
	"github.com/Civil/github2telegram/endpoints"
	"github.com/Civil/github2telegram/feeds"
	"github.com/lomik/zapwriter"
	_ "github.com/mattn/go-sqlite3"
)

const (
	currentSchemaVersion = 2
)

func initSqlite() db.Database {
	var err error
	logger := zapwriter.Logger("main")

	configs.Config.DB, err = sql.Open("sqlite3", configs.Config.DatabaseURL)
	if err != nil {
		logger.Fatal("unable to open database file",
			zap.Any("config", configs.Config),
			zap.Error(err),
		)
	}

	db := db.NewSQLite(configs.Config.DB)

	rows, err := configs.Config.DB.Query("SELECT version from 'schema_version' where id=1")
	if err != nil {
		if err.Error() == "no such table: schema_version" {
			_, err = configs.Config.DB.Exec(`
					CREATE TABLE IF NOT EXISTS 'schema_version' (
						'id' INTEGER PRIMARY KEY AUTOINCREMENT,
						'version' INTEGER NOT NULL
					);

					CREATE TABLE IF NOT EXISTS 'last_version' (
						'id' INTEGER PRIMARY KEY AUTOINCREMENT,
						'url' VARCHAR(255) NOT NULL,
						'filter' VARCHAR(255) NOT NULL,
						'last_tag' VARCHAR(255) NOT NULL DEFAULT '',
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

					INSERT INTO 'schema_version' (id, version) values (1, 2);
				`)
			if err != nil {
				logger.Fatal("failed to initialize database",
					zap.Any("config", configs.Config),
					zap.Error(err),
				)
			}
		} else {
			logger.Fatal("failed to query database version",
				zap.Error(err),
			)
		}
	} else {
		schemaVersion := int(0)
		for rows.Next() {
			err = rows.Scan(&schemaVersion)
			if err != nil {
				logger.Fatal("unable to fetch value",
					zap.Error(err),
				)
			}
		}
		rows.Close()

		if schemaVersion != currentSchemaVersion {
			switch schemaVersion {
			case 1:
				_, err = configs.Config.DB.Exec(`
ALTER TABLE last_version ADD COLUMN 'last_tag' VARCHAR(255) NOT NULL DEFAULT '';

UPDATE schema_version SET version = 2 WHERE id=1;
				`)

				if err != nil {
					logger.Fatal("failed to migrate database",
						zap.Int("databaseVersion", schemaVersion),
						zap.Int("upgradingTo", currentSchemaVersion),
						zap.Error(err),
					)
				}
				// 'last_tag' VARCHAR(255) NOT NULL DEFAULT '',
			default:
				// Don't know how to migrate from this version
				logger.Fatal("Unknown schema version specified",
					zap.Int("version", schemaVersion),
				)
			}
		}
	}

	return db
}

func main() {
	err := zapwriter.ApplyConfig([]zapwriter.Config{configs.DefaultLoggerConfig})
	if err != nil {
		log.Fatal("Failed to initialize logger with default configuration")

	}
	logger := zapwriter.Logger("main")

	configFile := flag.String("c", "config.yaml", "config file (json)")
	flag.Parse()

	if *configFile != "" {
		logger.Info("Will apply config from file",
			zap.String("config_file", *configFile),
		)
		cfgRaw, err := ioutil.ReadFile(*configFile)
		if err != nil {
			logger.Fatal("unable to load config file:",
				zap.Error(err),
			)
		}

		err = yaml.Unmarshal(cfgRaw, &configs.Config)
		if err != nil {
			logger.Fatal("error parsing config file",
				zap.Error(err),
			)
		}

		err = zapwriter.ApplyConfig(configs.Config.Logger)
		if err != nil {
			logger.Fatal("failed to apply config",
				zap.Any("config", configs.Config.Logger),
				zap.Error(err),
			)
		}
	}

	logger.Debug("loaded config", zap.Any("config", configs.Config))

	if configs.Config.DatabaseType != "sqlite3" {
		logger.Fatal("unsupported database",
			zap.String("database_type", configs.Config.DatabaseType),
			zap.Strings("supported_database_types", []string{"sqlite3"}),
		)
	}

	// TODO: Generalize to support other databases (e.x. mysql)
	var db db.Database
	if configs.Config.DatabaseType == "sqlite3" || configs.Config.DatabaseType == "sqlite" {
		db = initSqlite()
	}

	exitChan := make(chan struct{})

	configs.Config.Senders = make(map[string]configs.NotificationEndpoints)

	endpointsInitialized := 0
	for name, cfg := range configs.Config.Endpoints {
		logger.Debug("initializing endpoint",
			zap.Any("endpoint_config", cfg),
		)
		if cfg.Type == "telegram" {
			configs.Config.Senders[name], err = endpoints.InitializeTelegramEndpoint(cfg.Token, exitChan, db)
			if err != nil {
				logger.Fatal("Error initializing telegram endpoint",
					zap.Error(err),
					zap.Any("config", configs.Config),
				)
			}

			go configs.Config.Senders[name].Process()
			endpointsInitialized++
		} else {
			logger.Fatal("unknown type",
				zap.String("type", cfg.Type),
				zap.Strings("supported_types", []string{"telegram"}),
			)
		}
	}

	if endpointsInitialized == 0 {
		logger.Fatal("no endpoints initialized")
	}

	logger.Info("github2telegram initialized",
		zap.Any("config", configs.Config),
	)

	feedsListDB, err := db.ListFeeds()
	if err != nil {
		logger.Fatal("unknown error quering database",
			zap.Error(err),
		)
	}

	feedsList := make([]*feeds.Feed, 0, len(feedsListDB))
	for _, f := range feedsListDB {
		f2, err := feeds.NewFeed(f.Repo, f.Filter, f.Name, f.MessagePattern, db)
		if err != nil {
			continue
		}
		feedsList = append(feedsList, f2)
	}

	feeds.UpdateFeeds(feedsList)

	err = http.ListenAndServe(configs.Config.Listen, nil)
	if err != nil {
		logger.Fatal("error creating http server",
			zap.Error(err),
		)
	}
}

package main

import (
	"flag"
	"github.com/Civil/github2telegram/endpoints/telegram"
	"go.uber.org/zap"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"log"
	"net/http"
	_ "net/http/pprof"

	"github.com/Civil/github2telegram/configs"
	"github.com/Civil/github2telegram/db"
	"github.com/Civil/github2telegram/feeds"
	"github.com/lomik/zapwriter"
	_ "github.com/mattn/go-sqlite3"
)

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
	var database db.Database
	if configs.Config.DatabaseType == "sqlite3" || configs.Config.DatabaseType == "sqlite" {
		database = db.NewSQLite()
	}

	exitChan := make(chan struct{})

	configs.Config.Senders = make(map[string]configs.NotificationEndpoints)

	endpointsInitialized := 0
	for name, cfg := range configs.Config.Endpoints {
		logger.Debug("initializing endpoint",
			zap.Any("endpoint_config", cfg),
		)
		if cfg.Type == "telegram" {
			configs.Config.Senders[name], err = telegram.InitializeTelegramEndpoint(cfg.Token, exitChan, database,
				telegram.WithListenAddress(cfg.WebhookListenAddress),
				telegram.WithWebhookPath(cfg.WebhookPath),
				telegram.WithWebhookURL(cfg.WebhookURL),
			)
			if err != nil {
				logger.Fatal("Error initializing telegram endpoint",
					zap.Error(err),
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

	logger.Info("github2telegram initialized")

	feedsListDB, err := database.ListFeeds()
	if err != nil {
		logger.Fatal("unknown error quering database",
			zap.Error(err),
		)
	}

	logger.Debug("feedListDB Will Try to create", zap.Int("count", len(feedsListDB)))

	feedsList := make([]*feeds.Feed, 0, len(feedsListDB))
	for _, f := range feedsListDB {
		f2, err := feeds.NewFeed(f.Repo, f.Filter, f.Name, f.MessagePattern, database)
		if err != nil {
			logger.Error("feedListDB Creation failed", zap.Error(err))
			continue
		}
		feedsList = append(feedsList, f2)
	}
	logger.Debug("feedListDB Created", zap.Any("feeds", feedsListDB))

	feeds.UpdateFeeds(feedsList)

	err = http.ListenAndServe(configs.Config.Listen, nil)
	if err != nil {
		logger.Fatal("error creating http server",
			zap.Error(err),
		)
	}
}

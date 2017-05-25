package main

import (
	"fmt"
	"github.com/lomik/zapwriter"
	"github.com/mmcdole/gofeed"
	"go.uber.org/zap"
	"regexp"
	"time"
)

func processFeed(id int) {
	config.Lock()
	cfg := config.feedsConfig[id]
	config.Unlock()
	var err error
	logger := zapwriter.Logger("main").With(
		zap.String("feed_repo", cfg.Repo),
		zap.Int("id", id),
	)

	logger.Info("will process feed",
		zap.Int("id", id),
		zap.String("repo", cfg.Repo),
	)

	url := "https://github.com/" + cfg.Repo + "/releases.atom"

	// Initialize
	for i := range cfg.Filters {
		cfg.Filters[i].filterRegex, err = regexp.Compile(cfg.Filters[i].Filter)
		if err != nil {
			logger.Error("error parsing filter regexp",
				zap.Error(err),
			)
			return
		}
		cfg.Filters[i].lastUpdateTime = getLastUpdateTime(url, cfg.Filters[i].Filter)
	}

	fp := gofeed.NewParser()
	for {
		t0 := time.Now()
		for i := range cfg.Filters {
			cfg.Filters[i].filterProcessed = false
		}

		feed, err := fp.ParseURL(url)
		if err != nil {
			logger.Error("failed to parse feed",
				zap.Duration("runtime", time.Since(t0)),
				zap.Duration("sleep", cfg.PollingInterval),
				zap.Error(err),
			)

			time.Sleep(cfg.PollingInterval)
			continue
		}

		processedFilters := 0
		for _, item := range feed.Items {
			logger.Debug("processing item",
				zap.String("title", item.Title),
			)
			for i := range cfg.Filters {
				if cfg.Filters[i].lastUpdateTime.Unix() >= item.UpdatedParsed.Unix() {
					cfg.Filters[i].filterProcessed = true
				}
				if cfg.Filters[i].filterProcessed {
					logger.Debug("item already processed by this filter",
						zap.String("title", item.Title),
						zap.String("filter", cfg.Filters[i].Filter),
					)
					continue
				}
				logger.Debug("testing for filter",
					zap.String("filter", cfg.Filters[i].Filter),
				)
				if cfg.Filters[i].filterRegex.MatchString(item.Title) {
					notification := fmt.Sprintf(cfg.Filters[i].MessagePattern, cfg.Repo, item.Title)
					logger.Info("release tagged",
						zap.String("release", item.Title),
						zap.String("notification", notification),
					)

					methods, err := getNotificationMethods(cfg.Repo, cfg.Filters[i].Name)
					if err != nil {
						logger.Info("error",
							zap.Error(err),
						)
					} else {
						logger.Info("notifications",
							zap.Strings("methods", methods),
						)
						for _, m := range methods {
							config.senders[m].Send(cfg.Repo, cfg.Filters[i].Name, notification)
						}
					}

					cfg.Filters[i].filterProcessed = true
					cfg.Filters[i].lastUpdateTime = *item.UpdatedParsed
					updateLastUpdateTime(url, cfg.Filters[i].Filter, cfg.Filters[i].lastUpdateTime)
				}
			}

			if len(cfg.Filters) == processedFilters {
				break
			}
		}

		logger.Info("done",
			zap.Duration("runtime", time.Since(t0)),
			zap.Duration("sleep", cfg.PollingInterval),
		)
		time.Sleep(cfg.PollingInterval)
		config.Lock()
		cfg = config.feedsConfig[id]
		config.Unlock()

		for i := range cfg.Filters {
			if cfg.Filters[i].filterRegex == nil {
				cfg.Filters[i].filterRegex, err = regexp.Compile(cfg.Filters[i].Filter)
				if err != nil {
					logger.Error("error parsing filter regexp",
						zap.Error(err),
					)
					return
				}
				cfg.Filters[i].lastUpdateTime = getLastUpdateTime(url, cfg.Filters[i].Filter)
			}
		}
	}
}

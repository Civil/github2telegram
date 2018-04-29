package main

import (
	"time"

	"github.com/lomik/zapwriter"
	"github.com/lunny/html2md"
	"github.com/mmcdole/gofeed"
	"go.uber.org/zap"
	"math/rand"
	"strings"
)

type UpdateType int

const (
	NewRelease UpdateType = iota
	Retag
	DescriptionChange
)

type Update struct {
	Type   UpdateType
	Repo   string
	Filter string

	Title   string
	Content string
	Link    string
}

type Feed struct {
	Id             int
	Repo           string
	Filter         string
	Name           string
	MessagePattern string

	lastUpdateTime time.Time
	logger         *zap.Logger
	cfg            FeedsConfig
}

func NewFeed(id int, repo, filter, name, messagePattern string) (*Feed, error) {
	return &Feed{
		Id:             id,
		Repo:           repo,
		Filter:         filter,
		Name:           name,
		MessagePattern: messagePattern,

		lastUpdateTime: time.Unix(0, 0),

		logger: zapwriter.Logger("main").With(
			zap.String("feed_repo", repo),
			zap.Int("id", id),
		),
	}, nil
}

func (f *Feed) SetCfg(cfg FeedsConfig) {
	f.cfg = cfg
}

func (f *Feed) ProcessFeed() {
	cfg := f.cfg

	if len(cfg.Filters) == 0 {
		f.logger.Warn("no filters to process, exiting")
		return
	}

	url := "https://github.com/" + f.Repo + "/releases.atom"

	// Initialize
	for i := range cfg.Filters {
		cfg.Filters[i].lastUpdateTime = getLastUpdateTime(url, cfg.Filters[i].Filter)
	}

	fp := gofeed.NewParser()
	if cfg.PollingInterval == 0 {
		cfg.PollingInterval = config.PollingInterval
	}

	delay := time.Duration(rand.Int()) % cfg.PollingInterval
	t0 := time.Now()
	nextRun := t0.Add(delay)
	f.logger.Info("will process feed",
		zap.Duration("extra_delay", delay),
		zap.Time("nextRun", nextRun),
		zap.Int("filters", len(cfg.Filters)),
	)

	for {
		dt := time.Until(nextRun)
		if dt > 0 {
			time.Sleep(dt)
		}
		nextRun = nextRun.Add(cfg.PollingInterval)
		t0 = time.Now()
		for i := range cfg.Filters {
			cfg.Filters[i].filterProcessed = false
		}

		feed, err := fp.ParseURL(url)
		if err != nil {
			f.logger.Info("feed fetch failed ", zap.Duration("runtime", time.Since(t0)),
				zap.Duration("runtime", time.Since(t0)),
				zap.Time("nextRun", nextRun),
				zap.Time("now", t0),
				zap.Error(err),
			)
			continue
		}

		f.logger.Debug("received some data",
			zap.Int("items", len(feed.Items)),
		)

		processedFilters := 0
		for _, item := range feed.Items {
			f.logger.Debug("processing item",
				zap.String("title", item.Title),
				zap.Int("filters defined", len(cfg.Filters)),
			)
			for i := range cfg.Filters {
				f.logger.Debug("testing for filter",
					zap.String("filter", cfg.Filters[i].Filter),
					zap.Time("filter_last_update_time", cfg.Filters[i].lastUpdateTime),
					zap.Time("item_update_time", *item.UpdatedParsed),
				)
				if cfg.Filters[i].lastUpdateTime.Unix() >= item.UpdatedParsed.Unix() {
					cfg.Filters[i].filterProcessed = true
				}
				if cfg.Filters[i].filterProcessed {
					f.logger.Debug("item already processed by this filter",
						zap.String("title", item.Title),
						zap.String("filter", cfg.Filters[i].Filter),
					)
					continue
				}
				if cfg.Filters[i].filterRegex == nil {
					f.logger.Error("regex not defined for package",
						zap.Int("filter_id", i),
						zap.String("filter", cfg.Filters[i].Filter),
						zap.String("reason", "some bug caused filter not to be defined. This should never happen"),
					)
					continue
				}
				if cfg.Filters[i].filterRegex.MatchString(item.Title) {
					contentTruncated := false
					notification := cfg.Repo + " was tagged: " + item.Title + "\nLink: " + item.Link

					content := html2md.Convert(item.Content)
					if len(content) > 250 {
						content = content[:250] + "..."
						contentTruncated = true
					}
					content = strings.Replace(content, "```", "", 0)

					notification += "\nRelease notes:\n```\n" + content + "\n```"
					if contentTruncated {
						notification += "[More](" + item.Link + ")"
					}

					f.logger.Info("release tagged",
						zap.String("release", item.Title),
						zap.String("notification", notification),
						zap.String("content", item.Content),
					)

					methods, err := getNotificationMethods(cfg.Repo, cfg.Filters[i].Name)
					if err != nil {
						f.logger.Error("error sending notification",
							zap.Error(err),
						)
						continue
					}
					f.logger.Debug("notifications",
						zap.Strings("methods", methods),
					)
					for _, m := range methods {
						config.senders[m].Send(cfg.Repo, cfg.Filters[i].Name, notification)
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
		f.logger.Info("done",
			zap.Duration("runtime", time.Since(t0)),
			zap.Time("nextRun", nextRun),
			zap.Time("now", t0),
		)
	}
}

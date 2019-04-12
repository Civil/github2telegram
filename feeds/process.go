package feeds

import (
	"regexp"
	"time"

	"github.com/Civil/github2telegram/configs"
	"github.com/Civil/github2telegram/db"
	"github.com/pkg/errors"

	"math/rand"
	"strings"

	"github.com/lomik/zapwriter"
	"github.com/lunny/html2md"
	"github.com/mmcdole/gofeed"
	"go.uber.org/zap"
)

func UpdateFeeds(feeds []*Feed) {
	logger := zapwriter.Logger("updateFeeds")
	configs.Config.Lock()
	defer configs.Config.Unlock()

	for _, feed := range feeds {
		logger.Debug("will initialize feeds",
			zap.Any("feed", feed),
		)
		var cfg *configs.FeedsConfig
		for i := range configs.Config.FeedsConfig {
			if configs.Config.FeedsConfig[i].Repo == feed.Repo {
				cfg = configs.Config.FeedsConfig[i]
				break
			}
		}
		if cfg == nil {
			re, err := regexp.Compile(feed.Filter)
			if err != nil {
				logger.Error("failed to compile regex",
					zap.String("filter", feed.Filter),
					zap.Error(err),
				)
				continue
			}

			feed.cfg = configs.FeedsConfig{
				Repo:            feed.Repo,
				PollingInterval: configs.Config.PollingInterval,
				Filters: []configs.FiltersConfig{{
					Name:           feed.Name,
					Filter:         feed.Filter,
					MessagePattern: feed.MessagePattern,
					FilterRegex:    re,
				}},
			}

			configs.Config.FeedsConfig = append(configs.Config.FeedsConfig, &feed.cfg)
			continue
		}
		cfg.Filters = append(cfg.Filters, configs.FiltersConfig{
			Name:           feed.Name,
			Filter:         feed.Filter,
			MessagePattern: feed.MessagePattern,
		})
	}

	logger.Debug("feeds initialized",
		zap.Any("feeds", feeds),
	)

	for _, feed := range feeds {
		go func(f *Feed) {
			f.ProcessFeed()
		}(feed)
	}
}

type Feed struct {
	Id             int
	Repo           string
	Filter         string
	Name           string
	MessagePattern string

	db             db.Database
	lastUpdateTime time.Time
	logger         *zap.Logger
	cfg            configs.FeedsConfig
}

func NewFeed(repo, filter, name, messagePattern string, database db.Database) (*Feed, error) {

	id, err := database.AddFeed(name, repo, filter, messagePattern)
	if err != nil && err != db.ErrAlreadyExists {
		return nil, errors.Wrap(err, "error adding feed")
	}

	return &Feed{
		Id:             id,
		Repo:           repo,
		Filter:         filter,
		Name:           name,
		MessagePattern: messagePattern,

		db:             database,
		lastUpdateTime: time.Unix(0, 0),
		logger: zapwriter.Logger("main").With(
			zap.String("feed_repo", repo),
			zap.Int("id", id),
		),
	}, nil
}

func (f *Feed) SetCfg(cfg configs.FeedsConfig) {
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
		cfg.Filters[i].LastUpdateTime = f.db.GetLastUpdateTime(url, cfg.Filters[i].Filter)
		cfg.Filters[i].LastTag = f.db.GetLastTag(url, cfg.Filters[i].Filter)
	}

	fp := gofeed.NewParser()
	if cfg.PollingInterval == 0 {
		cfg.PollingInterval = configs.Config.PollingInterval
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
			cfg.Filters[i].FilterProcessed = false
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
					zap.Time("filter_last_update_time", cfg.Filters[i].LastUpdateTime),
					zap.Time("item_update_time", *item.UpdatedParsed),
				)
				if cfg.Filters[i].LastUpdateTime.Unix() >= item.UpdatedParsed.Unix() {
					cfg.Filters[i].FilterProcessed = true
				}
				if cfg.Filters[i].FilterProcessed {
					f.logger.Debug("item already processed by this filter",
						zap.String("title", item.Title),
						zap.String("filter", cfg.Filters[i].Filter),
					)
					continue
				}
				if cfg.Filters[i].FilterRegex == nil {
					f.logger.Error("regex not defined for package",
						zap.Int("filter_id", i),
						zap.String("filter", cfg.Filters[i].Filter),
						zap.String("reason", "some bug caused filter not to be defined. This should never happen"),
					)
					continue
				}
				if cfg.Filters[i].FilterRegex.MatchString(item.Title) {
					contentTruncated := false
					var changeType UpdateType
					var notification string

					// we check here if last tag hasnt changed
					if item.Title == cfg.Filters[i].LastTag {
						changeType = DescriptionChange
					} else {
						changeType = NewRelease
					}

					if changeType == NewRelease {
						notification += cfg.Repo + " was tagged: " + item.Title + "\nLink: " + item.Link
					} else if changeType == DescriptionChange {
						notification += cfg.Repo + " description  was changed: " + item.Title + "\nLink: " + item.Link
					}

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

					methods, err := f.db.GetNotificationMethods(cfg.Repo, cfg.Filters[i].Name)
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
						f.logger.Debug("will notify",
							zap.String("method", m),
							zap.Any("senders", configs.Config.Senders),
						)
						configs.Config.Senders[m].Send(cfg.Repo, cfg.Filters[i].Name, notification)
					}

					cfg.Filters[i].FilterProcessed = true
					cfg.Filters[i].LastUpdateTime = *item.UpdatedParsed
					f.db.UpdateLastUpdateTime(url, cfg.Filters[i].Filter, cfg.Filters[i].LastUpdateTime, item.Title)
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

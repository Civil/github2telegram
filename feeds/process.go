package feeds

import (
	"github.com/Civil/github2telegram/types"
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

var runningFeeds = make([]*Feed, 0)

func ForceProcessFeed(name string) {
	configs.Config.RLock()
	defer configs.Config.RUnlock()

	for _, f := range runningFeeds {
		if f.Repo == name {
			f.ForceProcess()
		}
	}
}

func UpdateFeeds(feeds []*Feed) {
	loggerRef := zapwriter.Logger("updateFeeds")
	configs.Config.Lock()
	defer configs.Config.Unlock()

	for _, feed := range feeds {
		logger := loggerRef.With(
			zap.Int("id", feed.Id),
			zap.String("repo", feed.Repo),
		)

		logger.Debug("will initialize feed",
			zap.Any("feed", feed),
		)
		var cfg *configs.FeedsConfig
		for i := range configs.Config.FeedsConfig {
			if configs.Config.FeedsConfig[i].Repo == feed.Repo {
				cfg = configs.Config.FeedsConfig[i]
				break
			}
		}

		re, err := regexp.Compile(feed.Filter)
		if err != nil {
			logger.Error("failed to compile regex",
				zap.String("filter", feed.Filter),
				zap.Error(err),
			)
			continue
		}

		// We were unable to find relevant configuration for this particular feed, we need to create it
		if cfg == nil {
			logger.Debug("creating first configuration for the repo")
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

		// Configuration was found, but this filter is new, we need to append it to existing repo
		logger.Debug("adding new configuration for existing repo")
		cfg.Filters = append(cfg.Filters, configs.FiltersConfig{
			Name:           feed.Name,
			Filter:         feed.Filter,
			MessagePattern: feed.MessagePattern,
			FilterRegex:    re,
		})

		feed.cfg.Filters = cfg.Filters
	}

	loggerRef.Debug("feeds initialized",
		zap.Any("feeds", feeds),
	)

	for _, feed := range feeds {
		// keep track of feeds that are currently running
		runningFeeds = append(runningFeeds, feed)
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

func (f *Feed) processSingleItem(cfg *configs.FeedsConfig, url string, item *gofeed.Item) {
	logger := f.logger.With(
		zap.String("item_title", item.Title),
		zap.Int("filters defined", len(cfg.Filters)),
		zap.Time("item_update_time", *item.UpdatedParsed),
	)
	logger.Debug("processing item")
	for i := range cfg.Filters {
		logger = logger.With(
			zap.Int("filter_id", i),
			zap.String("filter", cfg.Filters[i].Filter),
			zap.Time("filter_last_update_time", cfg.Filters[i].LastUpdateTime),
		)

		if cfg.Filters[i].FilterRegex == nil {
			logger.Error("regex not defined for package",
				zap.String("reason", "some bug caused filter not to be defined. This should never happen"),
			)
			continue
		}

		logger = logger.With(
			zap.String("filter_regex_string", cfg.Filters[i].FilterRegex.String()),
		)

		logger.Debug("will test for filter")

		if cfg.Filters[i].LastUpdateTime.Unix() >= item.UpdatedParsed.Unix() {
			cfg.Filters[i].FilterProcessed = true
		}

		if cfg.Filters[i].FilterProcessed {
			logger.Debug("item already processed by this filter")
			continue
		}

		if cfg.Filters[i].FilterRegex.MatchString(item.Title) {
			logger.Debug("filter matched")
			contentTruncated := false
			var changeType UpdateType
			var notification string

			// check if last tag haven't changed
			if item.Title == cfg.Filters[i].LastTag {
				changeType = DescriptionChange
				notification = types.MdReplacer.Replace(cfg.Repo) + " description changed: " + item.Title + "\nLink: " + types.MdReplacer.Replace(item.Link)
			} else {
				changeType = NewRelease
				notification = types.MdReplacer.Replace(cfg.Repo) + " tagged: " + types.MdReplacer.Replace(item.Title) + "\nLink: " + types.MdReplacer.Replace(item.Link)
			}

			content := html2md.Convert(item.Content)
			if len(content) > 250 {
				content = content[:250] + "\\.\\.\\."
				contentTruncated = true
			}
			content = strings.Replace(content, "```", "", 1)

			notification += "\nRelease notes:\n```\n" + content + "\n```"
			if contentTruncated {
				notification += "[More](" + item.Link + ")"
			}

			logger.Info("release tagged",
				zap.String("release", item.Title),
				zap.String("notification", notification),
				zap.String("content", item.Content),
				zap.Any("changeType", changeType),
			)

			methods, err := f.db.GetNotificationMethods(cfg.Repo, cfg.Filters[i].Name)
			if err != nil {
				logger.Error("error sending notification",
					zap.Error(err),
				)
				continue
			}
			logger.Debug("notifications",
				zap.Strings("methods", methods),
			)
			for _, m := range methods {
				logger.Debug("will notify",
					zap.String("method", m),
					zap.Any("senders", configs.Config.Senders),
				)
				err = configs.Config.Senders[m].Send(cfg.Repo, cfg.Filters[i].Name, notification)
				if err != nil {
					logger.Error("failed to send an update",
						zap.Error(err),
					)
				}
			}

			cfg.Filters[i].FilterProcessed = true
			cfg.Filters[i].LastUpdateTime = *item.UpdatedParsed
			cfg.Filters[i].LastTag = item.Title
			f.db.UpdateLastUpdateTime(url, cfg.Filters[i].Filter, item.Title, cfg.Filters[i].LastUpdateTime)
		} else {
			logger.Debug("filter doesn't match")
		}
	}
}

func (f *Feed) ForceProcess() {
	cfg := f.cfg

	if len(cfg.Filters) == 0 {
		f.logger.Warn("no filters to process, exiting",
			zap.Any("cfg", cfg),
		)
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

	f.logger.Info("force process triggered",
		zap.Int("filters", len(cfg.Filters)),
	)

	t0 := time.Now()
	for i := range cfg.Filters {
		cfg.Filters[i].FilterProcessed = false
	}

	feed, err := fp.ParseURL(url)
	if err != nil {
		f.logger.Error("feed fetch failed ", zap.Duration("runtime", time.Since(t0)),
			zap.Duration("runtime", time.Since(t0)),
			zap.Time("now", t0),
			zap.Error(err),
		)
		if strings.Contains(err.Error(), "404 Not Found") {
			err = f.db.RemoveFeed(f.Name, f.Repo, f.Filter, f.MessagePattern)
			if err != nil {
				f.logger.Error("error removing feed", zap.Error(err))
			}
		}
		return
	}

	f.logger.Debug("received some data",
		zap.Int("items", len(feed.Items)),
	)

	processedFilters := 0
	for _, item := range feed.Items {
		f.processSingleItem(&cfg, url, item)

		if len(cfg.Filters) == processedFilters {
			break
		}
	}
	f.logger.Info("done",
		zap.Duration("runtime", time.Since(t0)),
		zap.Time("now", t0),
	)
}

func (f *Feed) ProcessFeed() {
	cfg := f.cfg

	if len(cfg.Filters) == 0 {
		f.logger.Warn("no filters to process, exiting",
			zap.Any("cfg", cfg),
		)
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
			f.logger.Error("feed fetch failed ", zap.Duration("runtime", time.Since(t0)),
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
			f.processSingleItem(&cfg, url, item)

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

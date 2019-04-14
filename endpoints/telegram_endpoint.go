package endpoints

import (
	"github.com/Civil/github2telegram/configs"
	"github.com/Civil/github2telegram/db"
	"github.com/Civil/github2telegram/feeds"
	"github.com/lomik/zapwriter"
	"go.uber.org/zap"
	"gopkg.in/telegram-bot-api.v4"

	"fmt"
	"github.com/pkg/errors"
	"regexp"
	"strings"
)

const (
	TelegramEndpointName = "telegram"
)

type user struct {
	id       int
	username string
}

type handler func(tokens []string, update *tgbotapi.Update) error

type handlerWithDescription struct {
	f           handler
	description string
}

var errUnauthorized = errors.New("unauthorized action")

type TelegramEndpoint struct {
	api    *tgbotapi.BotAPI
	admins map[int64][]user
	db     db.Database

	logger   *zap.Logger
	commands map[string]handlerWithDescription

	exitChan <-chan struct{}
}

func InitializeTelegramEndpoint(token string, exitChan <-chan struct{}, database db.Database) (*TelegramEndpoint, error) {
	logger := zapwriter.Logger(TelegramEndpointName)
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		return nil, err
	}
	bot.Debug = true

	logger.Debug("Always authorized on account",
		zap.String("account", bot.Self.UserName),
	)

	e := &TelegramEndpoint{
		api:      bot,
		admins:   make(map[int64][]user),
		logger:   logger,
		exitChan: exitChan,
		db:       database,
	}

	e.commands = map[string]handlerWithDescription{
		"/new": {
			f:           e.handlerNew,
			description: "`/new repo filter_name filter_regexp` -- creates new available subscription" + `

Example:
  ` + "`/new lomik/go-carbon all ^V`" + `

  This will create repo named 'lomik/go-carbon', with filter called 'all' and regexp that will grab all tags that starts from capital 'V'`,
		},
		"/subscribe": {
			f:           e.handlerSubscribe,
			description: "`/subscribe repo filter_name` -- subscribe current channel to specific repo and filter" + `

Example:
  ` + "`/subscribe lomik/go-carbon all`" + `
`,
		},
		"/unsubscribe": {
			f:           e.handlerUnsubscribe,
			description: "`/unsubscribe repo filter_name`  -- unsubscribe current channel to specific repo and filter" + `

Example:
  ` + "`/unsubscribe lomik/go-carbon all`" + `
`,
		},
		"/list": {
			f:           e.handlerList,
			description: "`/list` -- lists all available repos",
		},
		"/help": {
			f:           e.handlerHelp,
			description: "`/help` -- display current help",
		},
	}

	return e, nil
}

func (e *TelegramEndpoint) Send(url, filter, message string) error {
	logger := e.logger.With(zap.String("handler", "send"))
	ids, err := e.db.GetEndpointInfo(TelegramEndpointName, url, filter)
	logger.Info("endpoint info",
		zap.Error(err),
		zap.Int64s("ids", ids),
	)
	if err != nil {
		return err
	}

	for _, id := range ids {
		e.sendMessage(id, 0, message)
	}

	return nil
}

func (e *TelegramEndpoint) sendMessage(chatID int64, messageID int, message string) {
	msg := tgbotapi.NewMessage(chatID, message)
	msg.ParseMode = tgbotapi.ModeMarkdown
	if messageID != 0 {
		msg.ReplyToMessageID = messageID
	}

	e.api.Send(msg)
}

// returns true if user can issue commands
func (e *TelegramEndpoint) checkAuthorized(update *tgbotapi.Update) bool {
	if !update.Message.Chat.IsPrivate() {
		logger := e.logger.With(zap.String("handler", "accessChecker"))
		chatID := update.Message.Chat.ID
		admins, ok := e.admins[chatID]
		if !ok {
			members, err := e.api.GetChatAdministrators(update.Message.Chat.ChatConfig())
			if err != nil {
				logger.Error("failed to get chat admins",
					zap.Error(err),
				)
			}
			for _, m := range members {
				admins = append(admins, user{m.User.ID, m.User.UserName})
			}
			e.admins[chatID] = admins
		}

		logger.Debug("list of admins",
			zap.Any("admins", admins),
		)

		for _, user := range admins {
			if user.id == update.Message.From.ID {
				return true
			}
		}
		return update.Message.From.UserName == configs.Config.AdminUsername
	}

	return true
}

func (e *TelegramEndpoint) handlerNew(tokens []string, update *tgbotapi.Update) error {
	if !e.checkAuthorized(update) {
		return errUnauthorized
	}
	if len(tokens) < 4 {
		return errors.New("Command require exactly 4 arguments\n\n" + e.commands["/new"].description)
	}

	repo := tokens[1]
	name := tokens[2]
	filter := tokens[3]
	pattern := "https://github.com/%v/releases/%v was tagged"

	_, err := regexp.Compile(filter)
	if err != nil {
		return errors.Wrap(err, "invalid regexp")
	}

	tmp := fmt.Sprintf(pattern, repo, "1.0")
	if strings.Contains(tmp, "%!") {
		return errors.New("Invalid message pattern!")
	}

	feed, err := feeds.NewFeed(repo, filter, name, pattern, e.db)
	if err != nil {
		return err
	}

	feeds.UpdateFeeds([]*feeds.Feed{feed})

	e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, "done")
	return nil
}

func isFilterExists(url, filterName string) bool {
	configs.Config.RLock()
	defer configs.Config.RUnlock()
	for _, feed := range configs.Config.FeedsConfig {
		if feed.Repo == url {
			for _, feedFilter := range feed.Filters {
				if feedFilter.Name == filterName {
					return true
					break
				}
			}
		}
	}
	return false
}

func (e *TelegramEndpoint) handlerSubscribe(tokens []string, update *tgbotapi.Update) error {
	logger := e.logger.With(zap.String("handler", "subscription"))
	if !e.checkAuthorized(update) {
		return errUnauthorized
	}

	if len(tokens) != 3 {
		return errors.New("/subscribe requires exactly 2 arguments.\n\n" + e.commands["/subscribe"].description)
	}

	url := tokens[1]
	filterName := tokens[2]

	found := isFilterExists(url, filterName)

	if !found {
		return errors.New("unknown combination of url and filter, use /list to get list of possible feeds")
	}

	chatID := update.Message.Chat.ID
	err := e.db.AddSubscribtion(TelegramEndpointName, url, filterName, chatID)
	if err != nil {
		if err == db.ErrAlreadyExists {
			return errors.New("already subscribed")
		}

		logger.Error("error adding subscription",
			zap.String("endpoint", TelegramEndpointName),
			zap.String("url", url),
			zap.String("filter_name", filterName),
			zap.Int64("chat_id", chatID),
			zap.Error(err),
		)
		return errors.New("error occurred while trying to subscribe")
	}

	e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, "successfully subscribed")
	return nil
}

func (e *TelegramEndpoint) handlerUnsubscribe(tokens []string, update *tgbotapi.Update) error {
	logger := e.logger.With(zap.String("handler", "unsubscribe"))
	if !e.checkAuthorized(update) {
		return errUnauthorized
	}

	if len(tokens) != 3 {
		return errors.New("/unsubscribe requires exactly 2 arguments\n\n" + e.commands["/unsubscribe"].description)
	}

	url := tokens[1]
	filterName := tokens[2]

	found := isFilterExists(url, filterName)

	if !found {
		return errors.New("Unknown combination of url and filter, use /list to get list of possible feeds")
	}

	chatID := update.Message.Chat.ID
	err := e.db.RemoveSubscribtion(TelegramEndpointName, url, filterName, chatID)
	if err != nil {
		logger.Error("error removing subscription",
			zap.String("endpoint", TelegramEndpointName),
			zap.String("url", url),
			zap.String("filter_name", filterName),
			zap.Int64("chat_id", chatID),
			zap.Error(err),
		)

		return errors.New("error occurred while trying to subscribe")
	}

	e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, "successfully unsubscribed")
	return nil
}

func (e *TelegramEndpoint) handlerList(tokens []string, update *tgbotapi.Update) error {
	response := "Configured feeds:\n"
	configs.Config.RLock()
	for _, feed := range configs.Config.FeedsConfig {
		for _, feedFilter := range feed.Filters {
			response = response + "`" + feed.Repo + "`: `" + feedFilter.Name + "`\n"
		}
	}
	configs.Config.RUnlock()

	e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, response)
	return nil
}

func (e *TelegramEndpoint) handlerHelp(tokens []string, update *tgbotapi.Update) error {
	response := ""
	for _, v := range e.commands {
		response = response + v.description + "\n\n==============================\n\n"
	}

	e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, response)
	return nil
}

func (e *TelegramEndpoint) Process() {
	logger := zapwriter.Logger(TelegramEndpointName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	for {
		select {
		case <-e.exitChan:
			return
		default:
		}
		updates, err := e.api.GetUpdatesChan(u)
		if err != nil {
			logger.Error("unknown error occurred",
				zap.Error(err),
			)
			continue
		}

		for update := range updates {
			if update.Message == nil {
				continue
			}

			logger.Debug("got message",
				zap.String("from", update.Message.From.UserName),
				zap.String("text", update.Message.Text),
			)

			tokens := strings.Split(update.Message.Text, " ")

			var m string
			cmd, ok := e.commands[tokens[0]]
			if !ok {
				tokens2 := strings.Split(tokens[0], "@")
				if len(tokens2) > 1 {
					if tokens2[1] == e.api.Self.UserName {
						cmd, ok = e.commands[tokens2[0]]
					}
				}
			}
			if ok {
				err = cmd.f(tokens, &update)
				if err != nil {
					m = err.Error()
				}
			}

			if m != "" {
				e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, m)
			}
		}
	}
}

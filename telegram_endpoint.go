package main

import (
	"github.com/lomik/zapwriter"
	"go.uber.org/zap"
	"gopkg.in/telegram-bot-api.v4"

	"fmt"
	"regexp"
	"strings"
)

const (
	telegeramApiUrl string = "https://api.telegram.org/bot%s/%s"
)

type TelegramEndpoint struct {
	api    *tgbotapi.BotAPI
	admins map[int64][]int
}

func initializeTelegramEndpoint(token string) *TelegramEndpoint {
	logger := zapwriter.Logger("telegram")
	log := logger
	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		logger.Fatal("Error initializing telegram bot",
			zap.Error(err),
			zap.String("token", token),
		)
	}
	bot.Debug = true

	log.Info("Authorized on account",
		zap.String("account", bot.Self.UserName),
	)

	return &TelegramEndpoint{
		api:    bot,
		admins: make(map[int64][]int),
	}
}

func (e *TelegramEndpoint) Send(url, filter, message string) error {
	logger := zapwriter.Logger("telegram_send")
	ids, err := getEndpointInfo("telegram", url, filter)
	logger.Info("endpoint info",
		zap.Error(err),
		zap.Int64s("ids", ids),
	)
	if err != nil {
		return err
	}

	for _, id := range ids {
		msg := tgbotapi.NewMessage(id, message)

		e.api.Send(msg)
	}

	return nil
}

func (e *TelegramEndpoint) sendMessage(chatID int64, messageID int, message string) {
	msg := tgbotapi.NewMessage(chatID, message)
	if messageID != 0 {
		msg.ReplyToMessageID = messageID
	}

	e.api.Send(msg)
}

func (e *TelegramEndpoint) checkUserAccess(update *tgbotapi.Update) bool {
	logger := zapwriter.Logger("accessChecker")
	chatID := update.Message.Chat.ID
	if !update.Message.Chat.IsPrivate() {
		admins, ok := e.admins[chatID]
		if !ok {
			members, err := e.api.GetChatAdministrators(update.Message.Chat.ChatConfig())
			if err != nil {
				logger.Error("failed to get chat admins",
					zap.Error(err),
				)
			}
			for _, m := range members {
				admins = append(admins, m.User.ID)
			}
			e.admins[chatID] = admins
		}

		for _, id := range admins {
			if id == update.Message.From.ID {
				return true
			}
		}
		return false
	}

	return true
}

func (e *TelegramEndpoint) Process() {
	logger := zapwriter.Logger("telegram")

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	for {
		updates, err := e.api.GetUpdatesChan(u)
		if err != nil {
			logger.Fatal("Unknown error occured",
				zap.Error(err),
			)
		}

		for update := range updates {
			if update.Message == nil {
				continue
			}

			logger.Info("got message",
				zap.String("from", update.Message.From.UserName),
				zap.String("text", update.Message.Text),
			)

			tokens := strings.Split(update.Message.Text, " ")

			var m string
			switch tokens[0] {
			case "/new":
				if !e.checkUserAccess(&update) {
					m = "Unauthorized action"
					break
				}
				if len(tokens) != 4 {
					m = "Usage: /new repo_name filter_name filter_regex [message_pattern (will replace first '%s' with feed name]"
					break
				}

				feed := Feed{
					Repo:   tokens[1],
					Name:   tokens[2],
					Filter: tokens[3],
				}

				m = "Success!"
				// TODO: Fix parser and allow to specify custom messages
				if len(tokens) == 5 {
					feed.MessagePattern = tokens[4]
				} else {
					feed.MessagePattern = "https://github.com/%v/releases/%v was tagged"
				}

				_, err := regexp.Compile(feed.Filter)
				if err != nil {
					m = "Invalid regexp"
					break
				}

				tmp := fmt.Sprintf(feed.MessagePattern, feed.Repo, "1.0")
				if strings.Contains(tmp, "%!") {
					m = "Invalid message pattern!"
					break
				}

				err = addFeed(feed)
				if err != nil {
					m = "Error adding feed: " + err.Error()
					break
				}
			case "/subscribe":
				if !e.checkUserAccess(&update) {
					m = "Unauthorized action"
					break
				}

				if len(tokens) != 3 {
					m = "/subscribe requires exactly 2 arguments"
					break
				}

				url := tokens[1]
				filterName := tokens[2]

				found := false

				config.RLock()
				for _, feed := range config.feedsConfig {
					if feed.Repo == url {
						for _, feedFilter := range feed.Filters {
							if feedFilter.Name == filterName {
								found = true
								break
							}
						}
						if found {
							break
						}
					}
				}
				config.RUnlock()

				if !found {
					m = "Unknown combination of url and filter, use /list to get list of possible feeds"
					break
				}

				chatID := update.Message.Chat.ID
				err = addSubscribtion("telegram", url, filterName, chatID)
				if err != nil {
					if err == errAlreadyExists {
						m = "Already subscribed"
						break
					}

					logger.Error("error adding subscription",
						zap.String("endpoint", "telegram"),
						zap.String("url", url),
						zap.String("filter_name", filterName),
						zap.Int64("chat_id", chatID),
						zap.Error(err),
					)
					m = "Error occured while trying to subscribe"
					break
				}

				m = "Success!"
			case "/unsubscribe":
				if !e.checkUserAccess(&update) {
					m = "Unauthorized action"
					break
				}

				if len(tokens) != 3 {
					m = "/unsubscribe requires exactly 3 arguments"
					break
				}

				url := tokens[1]
				filterName := tokens[2]

				found := false

				config.RLock()
				for _, feed := range config.feedsConfig {
					if feed.Repo == url {
						for _, feedFilter := range feed.Filters {
							if feedFilter.Name == filterName {
								found = true
								break
							}
						}
						if found {
							break
						}
					}
				}
				config.RUnlock()

				if !found {
					m = "Unknown combination of url and filter, use /list to get list of possible feeds"
					break
				}

				chatID := update.Message.Chat.ID
				err = removeSubscribtion("telegram", url, filterName, chatID)
				if err != nil {
					logger.Error("error removing subscription",
						zap.String("endpoint", "telegram"),
						zap.String("url", url),
						zap.String("filter_name", filterName),
						zap.Int64("chat_id", chatID),
						zap.Error(err),
					)

					m = "Error occured while trying to subscribe"
					break
				}

				m = "Success!"
			case "/subscriptions":
				m = "Not implemented yet!"
			case "/list":
				response := "Configured feeds:\n"
				config.RLock()
				for _, feed := range config.feedsConfig {
					for _, feedFilter := range feed.Filters {
						response = response + feed.Repo + ": " + feedFilter.Name + "\n"
					}
				}
				config.RUnlock()
				m = response
			case "/help":
				m = `supported commands:
	/new repo filter_name filter_regexp [message_pattern]
	/subscribe repo filter_name
	/unsubscribe repo filter_name
	/list`
			}

			if m != "" {
				e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, m)
			}
		}
	}
}

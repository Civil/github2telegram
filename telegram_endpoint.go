package main

import (
	"github.com/lomik/zapwriter"
	"go.uber.org/zap"
	"gopkg.in/telegram-bot-api.v4"

	"strings"
	"regexp"
	"fmt"
)

const (
	telegeramApiUrl string = "https://api.telegram.org/bot%s/%s"
)

type TelegramEndpoint struct {
	api *tgbotapi.BotAPI
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
		api: bot,
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

			if tokens[0] == "/new" {
				if len(tokens) != 4 {
					e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, "Usage: /new repo_name filter_name filter_regex [message_pattern (will replace firt '%s' with feed name]")
					continue
				}

				feed := Feed{
					Repo: tokens[1],
					Name: tokens[2],
					Filter: tokens[3],
				}

				// TODO: Fix parser and allow to specify custom messages
				if len(tokens) == 5 {
					feed.MessagePattern = tokens[4]
				} else {
					feed.MessagePattern = "%v: %v was tagged"
				}

				_, err := regexp.Compile(feed.Filter)
				if err != nil {
					e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, "Invalid regexp")
					continue
				}

				tmp := fmt.Sprintf(feed.MessagePattern, feed.Repo, "1.0")
				if strings.Contains(tmp, "%!") {
					e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, "Invalid message pattern!")
					continue
				}

				err = addFeed(feed)
				if err != nil {
					e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, "Error adding feed: " + err.Error())
					continue
				}
				e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, "Success!")
				continue
			}

			if tokens[0] == "/subscribe" {
				if len(tokens) != 3 {
					e.sendMessage(update.Message.Chat.ID, update.Message.MessageID,"/subscribe requires exactly 2 arguments")
					continue
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
					e.sendMessage(update.Message.Chat.ID, update.Message.MessageID,  "Unknown combination of url and filter, use /list to get list of possible feeds")
					continue
				}

				chatID := update.Message.Chat.ID
				err = addSubscribtion("telegram", url, filterName, chatID)
				if err != nil {
					if err == errAlreadyExists {
						msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Already subscribed")
						msg.ReplyToMessageID = update.Message.MessageID

						e.api.Send(msg)
						continue
					}

					e.sendMessage(update.Message.Chat.ID, update.Message.MessageID,"Error occured while trying to subscribe")

					logger.Error("error adding subscription",
						zap.String("endpoint", "telegram"),
						zap.String("url", url),
						zap.String("filter_name", filterName),
						zap.Int64("chat_id", chatID),
						zap.Error(err),
					)
					continue
				}

				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Success!")
				msg.ReplyToMessageID = update.Message.MessageID

				e.api.Send(msg)
				continue
			}

			if tokens[0] == "/unsubscribe" {
				if len(tokens) != 3 {
					e.sendMessage(update.Message.Chat.ID, update.Message.MessageID,"/unsubscribe requires exactly 3 arguments")
					continue
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
					e.sendMessage(update.Message.Chat.ID, update.Message.MessageID,"Unknown combination of url and filter, use /list to get list of possible feeds")
					continue
				}

				chatID := update.Message.Chat.ID
				err = removeSubscribtion("telegram", url, filterName, chatID)
				if err != nil {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Error occured while trying to subscribe")
					msg.ReplyToMessageID = update.Message.MessageID

					e.api.Send(msg)
					logger.Error("error removing subscription",
						zap.String("endpoint", "telegram"),
						zap.String("url", url),
						zap.String("filter_name", filterName),
						zap.Int64("chat_id", chatID),
						zap.Error(err),
					)
					continue
				}

				msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Success!")
				msg.ReplyToMessageID = update.Message.MessageID

				e.api.Send(msg)
				continue
			}

			if tokens[0] == "/list" {
				response := "Configured feeds:\n"
				config.RLock()
				for _, feed := range config.feedsConfig {
					for _, feedFilter := range feed.Filters {
						response = response + feed.Repo + ": " + feedFilter.Name + "\n"
					}
				}
				config.RUnlock()

				e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, response)

				continue
			}

			e.sendMessage(update.Message.Chat.ID, update.Message.MessageID,`supported commands:
	/new repo filter_name filter_regexp [message_pattern]
	/subscribe repo filter_name
	/unsubscribe repo filter_name
	/list`)
		}
	}
}
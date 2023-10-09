package telegram

import (
	"fmt"
	"github.com/Civil/github2telegram/configs"
	"github.com/Civil/github2telegram/db"
	"github.com/Civil/github2telegram/endpoints"
	"github.com/Civil/github2telegram/feeds"
	"github.com/Civil/github2telegram/types"
	"github.com/lomik/zapwriter"
	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	TelegramEndpointName = "telegram"
)

type user struct {
	id       int64
	username string
}

type handler func(tokens []string, update *telego.Update) error

type handlerWithDescription struct {
	f           handler
	description string
	hidden      bool
}

var errUnauthorized = errors.New("unauthorized action")

type TelegramEndpoint struct {
	api    *telego.Bot
	admins map[int64][]user
	db     db.Database

	logger   *zap.Logger
	commands map[string]handlerWithDescription

	exitChan    <-chan struct{}
	resendQueue chan *types.NotificationMessage

	tgLogger *tgLogger

	webhookURL  string
	webhookPath string
	useWebHook  bool
	listen      string

	selfUser string
}

func WithWebhookURL(url string) *endpoints.ConfigParams {
	return &endpoints.ConfigParams{
		Name:  "webhook_url",
		Value: url,
	}
}

func WithListenAddress(listen string) *endpoints.ConfigParams {
	return &endpoints.ConfigParams{
		Name:  "listen",
		Value: listen,
	}
}

func WithWebhookPath(path string) *endpoints.ConfigParams {
	return &endpoints.ConfigParams{
		Name:  "webhook_path",
		Value: path,
	}
}

func InitializeTelegramEndpoint(token string, exitChan <-chan struct{}, database db.Database, configParams ...*endpoints.ConfigParams) (*TelegramEndpoint, error) {
	logger := zapwriter.Logger(TelegramEndpointName)
	tgEndpointLogger := newTgLogger(logger, []string{token, "<TOKEN REDACTED>"})
	bot, err := telego.NewBot(token, telego.WithLogger(tgEndpointLogger))
	if err != nil {
		return nil, err
	}

	e := &TelegramEndpoint{
		api:         bot,
		admins:      make(map[int64][]user),
		logger:      logger,
		exitChan:    exitChan,
		resendQueue: make(chan *types.NotificationMessage, 1000),
		db:          database,
		tgLogger:    tgEndpointLogger,
	}

	for _, param := range configParams {
		switch param.Name {
		case "webhook_url":
			if len(param.Value) > 0 {
				e.webhookURL = param.Value
				e.useWebHook = true
			}
		case "listen":
			if len(param.Value) > 0 {
				e.listen = param.Value
			}
		case "webhook_path":
			if len(param.Value) > 0 {
				e.webhookPath = param.Value
			}
		default:
			return nil, fmt.Errorf("unknown config param %s", param.Name)
		}
	}

	if e.useWebHook {
		err = bot.SetWebhook(&telego.SetWebhookParams{
			URL: e.webhookURL + "/" + e.webhookPath + bot.Token(),
		})
		if err != nil {
			return nil, err
		}

		go func() {
			_ = bot.StartWebhook(e.listen)
		}()

		go func() {
			for {
				select {
				case <-e.exitChan:
					_ = bot.StopWebhook()
				}
			}
		}()
	}

	botUser, err := bot.GetMe()
	if err != nil {
		return nil, err
	}

	e.selfUser = botUser.Username

	logger.Debug("bot account",
		zap.String("username", botUser.Username),
	)

	e.commands = map[string]handlerWithDescription{
		"/new": {
			f: e.handlerNew,
			description: "```/new repo filter\\_name filter_regexp``` \\-\\- creates new available subscription" + `

Example:
  ` + "`/new lomik/go\\-carbon all ^V`" + `

  This will create repo named 'lomik/go\-carbon', with filter called 'all' and regexp that will grab all tags that starts from capital 'V'`,
		},
		"/subscribe": {
			f: e.handlerSubscribe,
			description: "`/subscribe repo filter\\_name` \\-\\- subscribe current channel to specific repo and filter" + `

Example:
  ` + "`/subscribe lomik/go-carbon all`" + `
`,
		},
		"/unsubscribe": {
			f: e.handlerUnsubscribe,
			description: "`/unsubscribe repo filter\\_name`  \\-\\- unsubscribe current channel to specific repo and filter" + `

Example:
  ` + "`/unsubscribe lomik/go\\-carbon all`" + `
`,
		},
		"/list": {
			f:           e.handlerList,
			description: "`/list` \\-\\- lists all available repos",
		},

		"/forceProcess": {
			hidden: true,
			f:      e.handlerForceProcess,
			description: "`/forceProcess repo` \\-\\- force process repository \\(can be only executed by account specified in config, for debug purpose only\\)" + `

Example:
  ` + "`/new lomik/go\\-carbon all ^V`" + `

  This will create repo named 'lomik/go\-carbon', with filter called 'all' and regexp that will grab all tags that starts from capital 'V'`,
		},
		"/help": {
			f:           e.handlerHelp,
			description: "`/help` \\-\\- display current help",
		},
	}

	messages, err := e.db.GetMessagesFromResentQueue()
	if err != nil {
		logger.Fatal("failed to get messages from resend queue", zap.Error(err))
	}

	go e.processResendQueue()

	for _, message := range messages {
		e.resendQueue <- message
	}

	return e, nil
}

func (e *TelegramEndpoint) processResendQueue() {
	logger := e.logger.With(zap.String("function", "processResendQueue"))
	for {
		select {
		case <-e.exitChan:
			messages := make([]*types.NotificationMessage, 0, len(e.resendQueue))
			for m := range e.resendQueue {
				messages = append(messages, m)
			}
			close(e.resendQueue)
			err := e.db.AddMessagesToResentQueue(messages)
			if err != nil {
				logger.Error("failed to add messages to resend queue", zap.Error(err), zap.Any("messages_in_queue", messages))
			}
			return
		case msg := <-e.resendQueue:
			err := e.sendMessage(msg.ChatID, 0, msg.Message)
			if err != nil {
				e.resendQueue <- msg
			}
			time.Sleep(time.Second)
		}
	}
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
		err = e.sendMessage(id, 0, message)
		if err != nil {
			if !e.checkError(err) {
				err2 := e.unsubscribe(logger, id, url, filter)
				if err2 != nil {
					logger.Warn("failed to unsubscribe",
						zap.Error(err2),
					)
				} else {
					logger.Warn("unsubscribed from chat",
						zap.Int64("ChatID", id),
						zap.String("url", url),
						zap.String("filter", filter),
						zap.String("reason", err.Error()),
					)
				}
				continue
			}
			e.resendQueue <- &types.NotificationMessage{id, message}
		}
	}

	return nil
}

func (e *TelegramEndpoint) sendMessage(chatID int64, messageID int, message string) error {
	msg := tu.Message(
		tu.ID(chatID),
		message,
	).WithParseMode(telego.ModeMarkdownV2)
	if messageID != 0 {
		msg = msg.WithReplyToMessageID(messageID)
	}

	_, err := e.api.SendMessage(msg)
	if err != nil {
		e.logger.Error("failed to send Message",
			zap.Any("msg", msg),
			zap.Error(err),
		)
	}
	return err
}

func (e *TelegramEndpoint) sendRawMessage(chatID int64, messageID int, message string) error {
	msg := tu.Message(
		tu.ID(chatID),
		message,
	)
	if messageID != 0 {
		msg = msg.WithReplyToMessageID(messageID)
	}

	_, err := e.api.SendMessage(msg)
	if err != nil {
		e.logger.Error("failed to send raw Message",
			zap.Any("msg", msg),
			zap.Error(err),
		)
	}
	return err
}

// returns true if user can issue commands
func (e *TelegramEndpoint) checkAuthorized(update *telego.Update) bool {
	logger := e.logger.With(zap.String("handler", "accessChecker"))
	if update.Message.Chat.Type != "private" {
		chatID := update.Message.Chat.ID
		admins, ok := e.admins[chatID]
		if !ok {
			params := &telego.GetChatAdministratorsParams{}
			members, err := e.api.GetChatAdministrators(params.WithChatID(tu.ID(chatID)))
			if err != nil {
				logger.Error("failed to get chat admins",
					zap.Error(err),
				)
				return false
			}
			for _, m := range members {
				admins = append(admins, user{m.MemberUser().ID, m.MemberUser().Username})
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
		return update.Message.From.Username == configs.Config.AdminUsername
	}

	return true
}

func (e *TelegramEndpoint) isRepoNameValid(repo string) error {
	validateRegexString := "^[-a-zA-Z0-9_]+$"
	repoNameSplit := strings.Split(repo, "/")
	if len(repoNameSplit) != 2 {
		return fmt.Errorf("repo name must follow format `org_or_user/repo_name`")
	}

	re, err := regexp.Compile(validateRegexString)
	if err != nil {
		return err
	}

	for i, s := range repoNameSplit {
		if !re.MatchString(s) {
			if i == 0 {
				return fmt.Errorf("user/org contains invalid characters, it must match regex `" + validateRegexString + "`")
			} else {
				return fmt.Errorf("repo-name contains invalid characters, it must match regex `" + validateRegexString + "`")
			}
		}
	}

	return nil
}

func (e *TelegramEndpoint) isFilterNameValid(filterName string) error {
	validateRegexString := "^[-a-zA-Z0-9_]+$"

	re, err := regexp.Compile(validateRegexString)
	if err != nil {
		return err
	}

	if !re.MatchString(filterName) {
		return fmt.Errorf("filter_name contains invalid characters, it must match regex `" + validateRegexString + "`")
	}

	return nil
}

func (e *TelegramEndpoint) handlerNew(tokens []string, update *telego.Update) error {
	if !e.checkAuthorized(update) {
		return errUnauthorized
	}
	if len(tokens) < 4 {
		return errors.New("command require exactly 4 arguments\n\n" + e.commands["/new"].description)
	}

	e.logger.Debug("got repo add request",
		zap.Strings("tokens", tokens),
	)

	repo := tokens[1]
	name := tokens[2]
	filter := tokens[3]
	err := e.isRepoNameValid(repo)
	if err != nil {
		return errors.Wrap(err, "invalid repo_name")
	}

	err = e.isFilterNameValid(name)
	if err != nil {
		return errors.Wrap(err, "invalid filter_name")
	}

	_, err = regexp.Compile(filter)
	if err != nil {
		return errors.Wrap(err, "invalid regexp")
	}

	resp, err := http.Get(fmt.Sprintf("https://github.com/%s/releases.atom", repo))
	if err != nil {
		return errors.Wrap(err, "repo is not accessible or doesn't exist")
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return errors.New(fmt.Sprintf("repo is not accessible or doesn't exist, http_code: %v", resp.StatusCode))
	}

	pattern := "https://github.com/%v/releases/%v was tagged"

	tmp := fmt.Sprintf(pattern, repo, "1.0")
	if strings.Contains(tmp, "%!") {
		return errors.New("Invalid Message pattern!")
	}

	feed, err := feeds.NewFeed(repo, filter, name, pattern, e.db)
	if err != nil {
		return err
	}

	feeds.UpdateFeeds([]*feeds.Feed{feed})

	return e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, "done")
}

func (e *TelegramEndpoint) handlerForceProcess(tokens []string, update *telego.Update) error {
	if update.Message.From.Username != configs.Config.AdminUsername {
		return errUnauthorized
	}
	if len(tokens) < 2 {
		return errors.New("Command require exactly 1 arguments\n\n" + e.commands["/forceProcess"].description)
	}

	repo := tokens[1]

	err := e.isRepoNameValid(repo)
	if err != nil {
		return errors.Wrap(err, "invalid repo_name")
	}

	feeds.ForceProcessFeed(repo)

	return e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, "done")
}

func isFilterExists(url, filterName string) bool {
	configs.Config.RLock()
	defer configs.Config.RUnlock()
	for _, feed := range configs.Config.FeedsConfig {
		if feed.Repo == url {
			for _, feedFilter := range feed.Filters {
				if feedFilter.Name == filterName {
					return true
				}
			}
		}
	}
	return false
}

func (e *TelegramEndpoint) handlerSubscribe(tokens []string, update *telego.Update) error {
	logger := e.logger.With(zap.String("handler", "subscription"))
	if !e.checkAuthorized(update) {
		return errUnauthorized
	}

	if len(tokens) != 3 {
		return errors.New("/subscribe requires exactly 2 arguments\n\n" + e.commands["/subscribe"].description)
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

	return e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, "successfully subscribed")
}

func (e *TelegramEndpoint) handlerUnsubscribe(tokens []string, update *telego.Update) error {
	logger := e.logger.With(zap.String("handler", "unsubscribe"))
	if !e.checkAuthorized(update) {
		return errUnauthorized
	}

	if len(tokens) != 3 {
		return errors.New("/unsubscribe requires exactly 2 arguments\n\n" + e.commands["/unsubscribe"].description)
	}

	url := tokens[1]
	filterName := tokens[2]

	chatID := update.Message.Chat.ID
	err := e.unsubscribe(logger, chatID, url, filterName)
	if err != nil {
		return err
	}

	return e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, "successfully unsubscribed")
}

func (e *TelegramEndpoint) unsubscribe(logger *zap.Logger, chatID int64, url string, filterName string) error {
	found := isFilterExists(url, filterName)

	if !found {
		return errors.New("Unknown combination of url and filter, use /list to get list of possible feeds")
	}

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
	return nil
}

func (e *TelegramEndpoint) handlerList(tokens []string, update *telego.Update) error {
	response := "Configured feeds:\n"
	configs.Config.RLock()
	for _, feed := range configs.Config.FeedsConfig {
		for _, feedFilter := range feed.Filters {
			response = response + "`" + feed.Repo + "`: `" + feedFilter.Name + "`\n"
		}
	}
	configs.Config.RUnlock()

	return e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, response)
}

func (e *TelegramEndpoint) handlerHelp(_ []string, update *telego.Update) error {
	response := ""
	for _, v := range e.commands {
		if v.hidden {
			e.logger.Debug("hidden command's help",
				zap.String("help", v.description),
			)
			continue
		}
		response = response + v.description + "\n\n\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\\=\n\n"
	}

	return e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, response)
}

var mdReplacer = strings.NewReplacer(
	".", "\\.",
)

// Return true if we need to continue
func (e *TelegramEndpoint) checkError(err error) bool {
	if strings.Contains(err.Error(), "chat not found") ||
		strings.Contains(err.Error(), "bot was kicked") ||
		strings.Contains(err.Error(), "not enough rights to send text messages") {
		return false
	}

	return true
}

func (e *TelegramEndpoint) Process() {
	logger := zapwriter.Logger(TelegramEndpointName)

	var updatesChan <-chan telego.Update
	var err error
	if e.useWebHook {
		updatesChan, err = e.api.UpdatesViaWebhook("/bot"+e.api.Token(), telego.WithWebhookBuffer(1024))
		if err != nil {
			logger.Fatal("failed to get updates via webhook", zap.Error(err))
		}
	} else {
		updatesChan, err = e.api.UpdatesViaLongPolling(
			&telego.GetUpdatesParams{},
			telego.WithLongPollingBuffer(1024),
			telego.WithLongPollingUpdateInterval(time.Second*0),
			telego.WithLongPollingRetryTimeout(time.Second*10),
		)
		if err != nil {
			logger.Fatal("failed to get updates via long polling", zap.Error(err))
		}
	}

	for {
		select {
		case <-e.exitChan:
			if !e.useWebHook {
				e.api.StopLongPolling()
			}
			return
		case update := <-updatesChan:
			if update.Message == nil {
				continue
			}

			logger.Debug("got Message",
				zap.String("from", update.Message.From.Username),
				zap.String("text", update.Message.Text),
			)

			tokens := strings.Split(update.Message.Text, " ")

			var m string
			cmd, ok := e.commands[tokens[0]]
			if !ok {
				tokens2 := strings.Split(tokens[0], "@")
				if len(tokens2) > 1 {
					if tokens2[1] == e.selfUser {
						cmd, ok = e.commands[tokens2[0]]
					}
				}
			}

			// It's possible that command had bot name explicitly mentioned, that is why that check is here
			if ok {
				err = cmd.f(tokens, &update)
				if err != nil {
					m = mdReplacer.Replace(err.Error())
				}
			}

			if m != "" {
				err = e.sendMessage(update.Message.Chat.ID, update.Message.MessageID, m)
			}
			if err != nil {
				logger.Error("error sending Message",
					zap.Int64("chat_id", update.Message.Chat.ID),
					zap.String("from", update.Message.From.Username),
					zap.String("text", update.Message.Text),
					zap.Error(err),
				)
			}
		default:
		}
	}
}

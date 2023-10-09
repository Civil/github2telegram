package db

import (
	"github.com/Civil/github2telegram/types"
	"time"
)

type Database interface {
	GetLastUpdateTime(url, filter string) time.Time
	GetLastTag(url, filter string) string
	UpdateLastUpdateTime(url, filter, tag string, t time.Time)

	AddFeed(name, repo, filter, messagePattern string) (int, error)
	GetFeed(name string) (*Feed, error)
	ListFeeds() ([]*Feed, error)
	RemoveFeed(name, repo, filter, messagePattern string) error

	// Subscriptions
	AddSubscribtion(endpoint, url, filter string, chatID int64) error
	RemoveSubscribtion(endpoint, url, filter string, chatID int64) error

	// Maintenance
	UpdateChatID(oldChatID, newChatID int64) error

	// Notification methods
	GetNotificationMethods(url, filter string) ([]string, error)

	// Endpoints
	GetEndpointInfo(endpoint, url, filter string) ([]int64, error)

	// Resend Queue
	AddMessagesToResentQueue(messages []*types.NotificationMessage) error
	GetMessagesFromResentQueue() ([]*types.NotificationMessage, error)
}

type Feed struct {
	Id             int
	Repo           string
	Filter         string
	Name           string
	MessagePattern string
}

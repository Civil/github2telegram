package db

import (
	"time"
)

type Database interface {
	GetLastUpdateTime(url, filter string) time.Time
	GetLastTag(url, filter string) string
	UpdateLastUpdateTime(url, filter string, t time.Time, tag string)

	AddFeed(name, repo, filter, messagePattern string) (int, error)
	GetFeed(name string) (*Feed, error)
	ListFeeds() ([]*Feed, error)

	// Subscriptions
	AddSubscribtion(endpoint, url, filter string, chatID int64) error
	RemoveSubscribtion(endpoint, url, filter string, chatID int64) error

	// Notification methods
	GetNotificationMethods(url, filter string) ([]string, error)

	// Endpoints
	GetEndpointInfo(endpoint, url, filter string) ([]int64, error)
}

type Feed struct {
	Id             int
	Repo           string
	Filter         string
	Name           string
	MessagePattern string
}

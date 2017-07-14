package main

import (
	"fmt"
	"github.com/lomik/zapwriter"
	"go.uber.org/zap"
	"time"
)

func getLastUpdateTime(url, filter string) time.Time {
	t, _ := time.Parse("2006-01-02", "1970-01-01")
	logger := zapwriter.Logger("get_date")
	stmt, err := config.db.Prepare("SELECT date from 'last_version' where url=? and filter=?")
	if err != nil {
		logger.Error("error creating statement",
			zap.Error(err),
		)
		return t
	}
	rows, err := stmt.Query(url, filter)
	if err != nil {
		logger.Error("error retreiving data",
			zap.Error(err),
		)
		return t
	}
	for rows.Next() {
		err = rows.Scan(&t)
		if err != nil {
			logger.Error("error retreiving data",
				zap.Error(err),
			)
			break
		}
	}
	rows.Close()
	return t
}

var errAlreadyExists error = fmt.Errorf("Already exists")

func addFeed(feed Feed) error {
	stmt, err := config.db.Prepare("SELECT id FROM 'feeds' where name=? and repo=?;")
	if err != nil {
		return err
	}

	rows, err := stmt.Query(feed.Name, feed.Repo)
	if err != nil {
		return err
	}

	if rows.Next() {
		rows.Close()
		return errAlreadyExists
	}
	rows.Close()

	stmt, err = config.db.Prepare("INSERT INTO 'feeds' (name, repo, filter, message_pattern) VALUES (?, ?, ?, ?)")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(feed.Name, feed.Repo, feed.Filter, feed.MessagePattern)
	if err != nil {
		return err
	}

	updateFeeds([]Feed{feed})

	return err
}

func getFeed(name string) (*Feed, error) {
	stmt, err := config.db.Prepare("SELECT name, repo, filter, message_pattern FROM 'feeds' WHERE name=?;")
	if err != nil {
		return nil, err
	}

	rows, err := stmt.Query(name)
	if err != nil {
		return nil, err
	}

	result := &Feed{}
	for rows.Next() {
		err = rows.Scan(&result.Name, &result.Repo, &result.Filter, &result.MessagePattern)
		if err != nil {
			continue
		}
	}
	rows.Close()

	return result, nil
}

func listFeeds() ([]Feed, error) {
	rows, err := config.db.Query("SELECT name, repo, filter, message_pattern FROM 'feeds';")
	if err != nil {
		return nil, err
	}

	var result []Feed
	for rows.Next() {
		tmp := Feed{}
		err = rows.Scan(&tmp.Name, &tmp.Repo, &tmp.Filter, &tmp.MessagePattern)
		if err != nil {
			continue
		}
		result = append(result, tmp)
	}
	rows.Close()

	return result, nil
}

func addSubscribtion(endpoint, url, filter string, chatID int64) error {
	stmt, err := config.db.Prepare("SELECT chat_id FROM 'subscriptions' where endpoint=? and url=? and filter=? and chat_id=?;")
	if err != nil {
		return err
	}

	rows, err := stmt.Query(endpoint, url, filter, chatID)
	if err != nil {
		return err
	}

	if rows.Next() {
		rows.Close()
		return errAlreadyExists
	}
	rows.Close()

	stmt, err = config.db.Prepare("INSERT INTO 'subscriptions' (endpoint, url, filter, chat_id) VALUES (?, ?, ?, ?)")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(endpoint, url, filter, chatID)

	return err
}

func removeSubscribtion(endpoint, url, filter string, chatID int64) error {
	stmt, err := config.db.Prepare("DELETE FROM 'subscriptions' WHERE endpoint=? and url=? and filter=? and chat_id=?")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(endpoint, url, filter, chatID)

	return err
}

func getNotificationMethods(url, filter string) ([]string, error) {
	logger := zapwriter.Logger("get_notification_method")
	logger.Info("",
		zap.String("url", url),
		zap.String("filter", filter),
	)
	stmt, err := config.db.Prepare("SELECT DISTINCT endpoint FROM 'subscriptions' where url=? and filter=?;")
	if err != nil {
		return nil, err
	}

	rows, err := stmt.Query(url, filter)
	if err != nil {
		return nil, err
	}

	var result []string
	var tmp string
	for rows.Next() {
		err = rows.Scan(&tmp)
		if err != nil {
			logger.Error("error retreiving data",
				zap.Error(err),
			)
			continue
		}
		result = append(result, tmp)
	}
	rows.Close()

	return result, nil
}

func getEndpointInfo(endpoint, url, filter string) ([]int64, error) {
	logger := zapwriter.Logger("get_endpoint_info")
	logger.Info("",
		zap.String("endpoint", endpoint),
		zap.String("url", url),
		zap.String("filter", filter),
	)
	stmt, err := config.db.Prepare("SELECT chat_id FROM 'subscriptions' where endpoint=? and url=? and filter=?;")
	if err != nil {
		return nil, err
	}

	rows, err := stmt.Query(endpoint, url, filter)
	if err != nil {
		return nil, err
	}

	var result []int64
	var tmp int64
	for rows.Next() {
		err = rows.Scan(&tmp)
		if err != nil {
			logger.Error("error retreiving data",
				zap.Error(err),
			)
			continue
		}
		result = append(result, tmp)
	}
	rows.Close()

	return result, nil
}

func updateLastUpdateTime(url, filter string, t time.Time) {
	logger := zapwriter.Logger("updater")
	id := -1
	stmt, err := config.db.Prepare("SELECT id FROM 'last_version' where url=? and filter=?;")
	if err != nil {
		logger.Error("error creating statement to get current id",
			zap.Error(err),
		)
		return
	}
	rows, err := stmt.Query(url, filter)
	if err != nil {
		logger.Error("error retreiving data",
			zap.Error(err),
		)
		return
	}
	for rows.Next() {
		err = rows.Scan(&id)
		if err != nil {
			logger.Error("error retreiving data",
				zap.Error(err),
			)
			break
		}
	}
	rows.Close()

	if id != -1 {
		stmt, err = config.db.Prepare("UPDATE 'last_version' SET date=? where id=?")
	} else {
		stmt, err = config.db.Prepare("INSERT INTO 'last_version' (url, filter, date) VALUES (?, ?, ?)")
	}
	if err != nil {
		logger.Error("error creating statement",
			zap.Error(err),
		)
		return
	}

	if id != -1 {
		_, err = stmt.Exec(t, id)
	} else {
		_, err = stmt.Exec(url, filter, t)
	}
	if err != nil {
		logger.Error("error updating data",
			zap.Error(err),
		)
		return
	}
}

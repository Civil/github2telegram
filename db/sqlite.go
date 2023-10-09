package db

import (
	"database/sql"
	"fmt"
	"github.com/Civil/github2telegram/types"
	"time"

	"github.com/Civil/github2telegram/configs"
	"github.com/lomik/zapwriter"
	"go.uber.org/zap"
)

const (
	currentSchemaVersion = 3
)

type SQLite struct {
	db *sql.DB
}

func initSqlite() Database {
	var err error
	logger := zapwriter.Logger("main")

	configs.Config.DB, err = sql.Open("sqlite3", configs.Config.DatabaseURL)
	if err != nil {
		logger.Fatal("unable to open database file",
			zap.String("file", configs.Config.DatabaseURL),
			zap.Error(err),
		)
	}

	db := &SQLite{
		db: configs.Config.DB,
	}

	rows, err := configs.Config.DB.Query("SELECT version from 'schema_version' where id=1")
	if err != nil {
		if err.Error() == "no such table: schema_version" {
			_, err = configs.Config.DB.Exec(`
					CREATE TABLE IF NOT EXISTS 'schema_version' (
						'id' INTEGER PRIMARY KEY AUTOINCREMENT,
						'version' INTEGER NOT NULL
					);

					CREATE TABLE IF NOT EXISTS 'last_version' (
						'id' INTEGER PRIMARY KEY AUTOINCREMENT,
						'url' VARCHAR(255) NOT NULL,
						'filter' VARCHAR(255) NOT NULL,
						'last_tag' VARCHAR(255) NOT NULL DEFAULT '',
						'date' DATE NOT NULL
					);

					CREATE TABLE IF NOT EXISTS 'subscriptions' (
						'id' INTEGER PRIMARY KEY AUTOINCREMENT,
						'chat_id' Int64,
						'endpoint' VARCHAR(255) NOT NULL,
						'url' VARCHAR(255) NOT NULL,
						'filter' VARCHAR(255) NOT NULL
					);

					CREATE TABLE IF NOT EXISTS 'feeds' (
						'id' INTEGER PRIMARY KEY AUTOINCREMENT,
						'repo' VARCHAR(255) NOT NULL,
						'filter' VARCHAR(255) NOT NULL,
						'name' VARCHAR(255) NOT NULL,
						'message_pattern' VARCHAR(255) NOT NULL
					);

					CREATE TABLE IF NOT EXISTS 'resend_queue' (
					    'id' INTEGER PRIMARY KEY AUTOINCREMENT,
                        'chat_id' Int64,
                        'message' TEXT NOT NULL
					);

					INSERT INTO 'schema_version' (id, version) values (1, 3);
				`)
			if err != nil {
				logger.Fatal("failed to initialize database",
					zap.Error(err),
				)
			}
		} else {
			logger.Fatal("failed to query database version",
				zap.Error(err),
			)
		}
	} else {
		schemaVersion := int(0)
		for rows.Next() {
			err = rows.Scan(&schemaVersion)
			if err != nil {
				logger.Fatal("unable to fetch value",
					zap.Error(err),
				)
			}
		}
		rows.Close()

		if schemaVersion == 1 {
			_, err = configs.Config.DB.Exec(`
ALTER TABLE last_version ADD COLUMN 'last_tag' VARCHAR(255) NOT NULL DEFAULT '';

UPDATE schema_version SET version = 2 WHERE id=1;
				`)

			if err != nil {
				logger.Fatal("failed to migrate database",
					zap.Int("databaseVersion", schemaVersion),
					zap.Int("upgradingTo", currentSchemaVersion),
					zap.Error(err),
				)
			}

			_, err = configs.Config.DB.Exec(`
UPDATE schema_version SET version = 2 WHERE id=1;`)
			if err != nil {
				logger.Fatal("failed to migrate database",
					zap.Int("databaseVersion", schemaVersion),
					zap.Int("upgradingTo", currentSchemaVersion),
					zap.Error(err),
				)
			}

			// We've successfully upgraded to schema version 2.
			schemaVersion = 2
		}

		if schemaVersion == 2 {
			_, err = configs.Config.DB.Exec(`	CREATE TABLE IF NOT EXISTS 'resend_queue' (
					    'id' INTEGER PRIMARY KEY AUTOINCREMENT,
                        'chat_id' Int64,
                        'message' TEXT NOT NULL
					);`)
			if err != nil {
				logger.Fatal("failed to migrate database",
					zap.Int("databaseVersion", schemaVersion),
					zap.Int("upgradingTo", currentSchemaVersion),
					zap.Error(err),
				)
			}

			_, err = configs.Config.DB.Exec(`
UPDATE schema_version SET version = 3 WHERE id=1;`)
			if err != nil {
				logger.Fatal("failed to migrate database",
					zap.Int("databaseVersion", schemaVersion),
					zap.Int("upgradingTo", currentSchemaVersion),
					zap.Error(err),
				)
			}

			// We've successfully upgraded to schema version 3.
			schemaVersion = 3
		}

		if schemaVersion != currentSchemaVersion {
			// Don't know how to migrate from this version
			logger.Fatal("Unknown schema version specified",
				zap.Int("version", schemaVersion),
			)
		}
	}

	return db
}

func NewSQLite() Database {
	return initSqlite()
}

var ErrAlreadyExists error = fmt.Errorf("already exists")

// GetLastUpdateTime - gets Last Update Time
func (d *SQLite) GetLastUpdateTime(url, filter string) time.Time {
	t, _ := time.Parse("2006-01-02", "1970-01-01")
	logger := zapwriter.Logger("get_date")
	stmt, err := d.db.Prepare("SELECT date from 'last_version' where url=? and filter=?")
	if err != nil {
		logger.Error("error creating statement",
			zap.Error(err),
		)
		return t
	}
	rows, err := stmt.Query(url, filter)
	if err != nil {
		logger.Error("error retrieving data",
			zap.Error(err),
		)
		return t
	}
	for rows.Next() {
		err = rows.Scan(&t)
		if err != nil {
			logger.Error("error retrieving data",
				zap.Error(err),
			)
			break
		}
	}
	_ = rows.Close()
	return t
}

// GetLastTag - gets Last Tag
func (d *SQLite) GetLastTag(url, filter string) string {
	var tag string
	logger := zapwriter.Logger("get_last_tag")
	stmt, err := d.db.Prepare("SELECT last_tag from 'last_version' where url=? and filter=?")
	if err != nil {
		logger.Error("error creating statement",
			zap.Error(err),
		)
		return tag
	}
	rows, err := stmt.Query(url, filter)
	if err != nil {
		logger.Error("error retrieving data",
			zap.Error(err),
		)
		return tag
	}
	for rows.Next() {
		err = rows.Scan(&tag)
		if err != nil {
			logger.Error("error retrieving data",
				zap.Error(err),
			)
			break
		}
	}
	_ = rows.Close()
	return tag
}

func (d *SQLite) AddFeed(name, repo, filter, messagePattern string) (int, error) {
	stmt, err := d.db.Prepare("SELECT id FROM 'feeds' where name=? and repo=?;")
	if err != nil {
		return -1, err
	}

	rows, err := stmt.Query(name, repo)
	if err != nil {
		return -1, err
	}

	var id int
	if rows.Next() {
		err = rows.Scan(&id)
		if err != nil {
			return -1, err
		}
		_ = rows.Close()
		return id, ErrAlreadyExists
	}
	_ = rows.Close()

	stmt, err = d.db.Prepare("INSERT INTO 'feeds' (name, repo, filter, message_pattern) VALUES (?, ?, ?, ?)")
	if err != nil {
		return -1, err
	}

	_, err = stmt.Exec(name, repo, filter, messagePattern)
	if err != nil {
		return -1, err
	}

	stmt, err = d.db.Prepare("SELECT id FROM 'feeds' where name=? and repo=?;")
	if err != nil {
		return -1, err
	}

	rows, err = stmt.Query(name, repo)
	if err != nil {
		return -1, err
	}

	if rows.Next() {
		err = rows.Scan(&id)
		if err != nil {
			return -1, err
		}
	}
	_ = rows.Close()

	return id, nil
}

func (d *SQLite) GetFeed(name string) (*Feed, error) {
	stmt, err := d.db.Prepare("SELECT name, repo, filter, message_pattern FROM 'feeds' WHERE name=?;")
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
	_ = rows.Close()

	return result, nil
}

func (d *SQLite) ListFeeds() ([]*Feed, error) {
	rows, err := d.db.Query("SELECT id, name, repo, filter, message_pattern FROM 'feeds';")
	if err != nil {
		return nil, err
	}

	var result []*Feed
	var id int
	var name, repo, filter, pattern string
	for rows.Next() {
		err = rows.Scan(&id, &name, &repo, &filter, &pattern)
		if err != nil {
			continue
		}

		f := &Feed{id, repo, filter, name, pattern}
		result = append(result, f)
	}
	_ = rows.Close()

	return result, nil
}

func (d *SQLite) RemoveFeed(name, repo, filter, messagePattern string) error {
	logger := zapwriter.Logger("remove_feed")
	stmt, err := d.db.Prepare("DELETE FROM 'feeds' WHERE name=? and repo=? and filter=? and message_pattern=?")
	if err != nil {
		logger.Error("error creating statement",
			zap.Error(err),
		)
		return err
	}

	_, err = stmt.Exec(name, repo, filter, messagePattern)
	if err != nil {
		logger.Error("error removing subscription",
			zap.Error(err),
		)
	}

	return err
}

func (d *SQLite) UpdateChatID(oldChatID, newChatID int64) error {
	stmt, err := d.db.Prepare("UPDATE 'subscriptions' SET chat_id=? WHERE chat_id=?")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(newChatID, oldChatID)
	return err
}

func (d *SQLite) AddSubscribtion(endpoint, url, filter string, chatID int64) error {
	stmt, err := d.db.Prepare("SELECT chat_id FROM 'subscriptions' where endpoint=? and url=? and filter=? and chat_id=?;")
	if err != nil {
		return err
	}

	rows, err := stmt.Query(endpoint, url, filter, chatID)
	if err != nil {
		return err
	}

	if rows.Next() {
		_ = rows.Close()
		return ErrAlreadyExists
	}
	_ = rows.Close()

	stmt, err = d.db.Prepare("INSERT INTO 'subscriptions' (endpoint, url, filter, chat_id) VALUES (?, ?, ?, ?)")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(endpoint, url, filter, chatID)

	return err
}

func (d *SQLite) RemoveSubscribtion(endpoint, url, filter string, chatID int64) error {
	stmt, err := d.db.Prepare("DELETE FROM 'subscriptions' WHERE endpoint=? and url=? and filter=? and chat_id=?")
	if err != nil {
		return err
	}

	_, err = stmt.Exec(endpoint, url, filter, chatID)

	return err
}

func (d *SQLite) GetNotificationMethods(url, filter string) ([]string, error) {
	logger := zapwriter.Logger("get_notification_method")
	logger.Info("",
		zap.String("url", url),
		zap.String("filter", filter),
	)
	stmt, err := d.db.Prepare("SELECT DISTINCT endpoint FROM 'subscriptions' where url=? and filter=?;")
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
			logger.Error("error retrieving data",
				zap.Error(err),
			)
			continue
		}
		result = append(result, tmp)
	}
	_ = rows.Close()

	return result, nil
}

func (d *SQLite) GetEndpointInfo(endpoint, url, filter string) ([]int64, error) {
	logger := zapwriter.Logger("get_endpoint_info")
	logger.Info("",
		zap.String("endpoint", endpoint),
		zap.String("url", url),
		zap.String("filter", filter),
	)
	stmt, err := d.db.Prepare("SELECT chat_id FROM 'subscriptions' where endpoint=? and url=? and filter=?;")
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
			logger.Error("error retrieving data",
				zap.Error(err),
			)
			continue
		}
		result = append(result, tmp)
	}
	_ = rows.Close()

	return result, nil
}

func (d *SQLite) UpdateLastUpdateTime(url, filter, tag string, t time.Time) {
	logger := zapwriter.Logger("updater")
	id := -1
	stmt, err := d.db.Prepare("SELECT id FROM 'last_version' where url=? and filter=?;")
	if err != nil {
		logger.Error("error creating statement to get current id",
			zap.Error(err),
		)
		return
	}
	rows, err := stmt.Query(url, filter)
	if err != nil {
		logger.Error("error retrieving data",
			zap.Error(err),
		)
		return
	}

	for rows.Next() {
		err = rows.Scan(&id)
		if err != nil {
			logger.Error("error retrieving data",
				zap.Error(err),
			)
			break
		}
	}
	_ = rows.Close()

	if id != -1 {
		stmt, err = d.db.Prepare("UPDATE 'last_version' SET date=?, last_tag=? where id=?")
	} else {
		stmt, err = d.db.Prepare("INSERT INTO 'last_version' (url, filter, date, last_tag) VALUES (?, ?, ?, ?)")
	}
	if err != nil {
		logger.Error("error creating statement",
			zap.Error(err),
		)
		return
	}

	if id != -1 {
		_, err = stmt.Exec(t, tag, id)
	} else {
		_, err = stmt.Exec(url, filter, t, tag)
	}
	if err != nil {
		logger.Error("error updating data",
			zap.Error(err),
		)
		return
	}
}

func (db *SQLite) AddMessagesToResentQueue(messages []*types.NotificationMessage) error {
	logger := zapwriter.Logger("add_messages_to_resent_queue")
	stmt, err := db.db.Prepare("INSERT INTO 'resend_queue' (chat_id, message) VALUES (?, ?)")
	if err != nil {
		logger.Error("error creating statement",
			zap.Error(err),
		)
		return err
	}

	for _, m := range messages {
		_, err = stmt.Exec(m.ChatID, m.Message)
		if err != nil {
			logger.Error("error updating data",
				zap.Error(err),
			)
			return err
		}
	}
	return nil
}

func (db *SQLite) GetMessagesFromResentQueue() ([]*types.NotificationMessage, error) {
	logger := zapwriter.Logger("get_messages_from_resent_queue")
	stmt, err := db.db.Prepare("SELECT chat_id, message FROM 'resend_queue'")
	if err != nil {
		logger.Error("error creating statement",
			zap.Error(err),
		)
		return nil, err
	}
	rows, err := stmt.Query()
	if err != nil {
		logger.Error("error retrieving data",
			zap.Error(err),
		)
		return nil, err
	}
	results := make([]*types.NotificationMessage, 0)
	for rows.Next() {
		res := &types.NotificationMessage{}
		err = rows.Scan(&res.ChatID, &res.Message)
		if err != nil {
			logger.Error("error retrieving data",
				zap.Error(err),
			)
			continue
		}
		results = append(results, res)
	}
	_ = rows.Close()

	return nil, nil
}

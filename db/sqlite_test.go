package db

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Civil/github2telegram/configs"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/suite"
)

const (
	testDbName = "testgithub2telegram.dbdata"
)

type SQLiteSuite struct {
	suite.Suite
	db Database
}

func (s *SQLiteSuite) SetupSuite() {
	configs.Config.DatabaseURL = fmt.Sprintf("./%s", testDbName)
	s.db = NewSQLite()
}

func (s *SQLiteSuite) TearDownSuite() {
	os.Remove(testDbName)
}

func (s *SQLiteSuite) TestUpdateLastUpdateTime() {
	//url, filter, tag string, t time.Time
	t := time.Date(2018, time.June, 12, 7, 8, 0, 0, time.UTC)
	url := "https://test.com/test1"
	filter := ""
	version := "0.1.1"
	s.db.UpdateLastUpdateTime(url, filter, version, t)

	tnew := s.db.GetLastUpdateTime(url, filter)

	r := s.Require()
	r.Equal(t, tnew)
}

func (s *SQLiteSuite) TestUpdateLastTag() {
	//url, filter, tag string, t time.Time
	t := time.Date(2018, time.June, 12, 7, 8, 0, 0, time.UTC)
	url := "https://bla.com/test2"
	filter := ""
	version := "0.2.3"
	s.db.UpdateLastUpdateTime(url, filter, version, t)

	versionnew := s.db.GetLastTag(url, filter)

	r := s.Require()
	r.Equal(version, versionnew)
}

func TestDBSuite(t *testing.T) {
	ts := &SQLiteSuite{}
	suite.Run(t, ts)
}

package database

import (
	"database/sql"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func initializeConnection(dbName string, user string, password string) *sql.DB {
	dataSourceName := user + ":" + password + "/@" + dbName

	db, err := sql.Open("mysql", dataSourceName)
	if err != nil {
		panic(err)
	}

	db.SetConnMaxLifetime(time.Minute * 3)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(10)

	return db
}

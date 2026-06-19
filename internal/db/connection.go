package db

import (
	"database/sql"
	"log"
)

func OpenDB() *sql.DB {
	db, err := sql.Open("sqlite3", "ciri.db")
	if err != nil {
		log.Fatal(err)
	}

	// Crucial for SQLite: limit to 1 connection for writes
	db.SetMaxOpenConns(1)

	return db
}

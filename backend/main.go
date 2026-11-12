package main

import (
	"database/sql"
	_ "embed"
	"log"
	"net/http"
	"os"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed sql/schema.sql
var createTables string

// Implement auth
func run() error {
	path := os.Getenv("DB_PATH")
	if path == "" {
		path = "laundry.sqlite"
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	// Keep foreign-key settings and serialized workflow transactions on one connection.
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;" + createTables); err != nil {
		return err
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if err := bootstrapAdmin(db); err != nil {
		return err
	}
	server := &http.Server{Addr: ":" + port, Handler: newHandler(db), ReadHeaderTimeout: 5 * time.Second}
	log.Printf("Smart Laundry listening on http://localhost:%s", port)
	return server.ListenAndServe()
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

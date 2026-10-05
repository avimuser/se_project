package main

import (
	"database/sql"
	"fmt"
	"log"
	"net/http"

	"se_project/dbutils"

	_ "embed"

	_ "modernc.org/sqlite"
)

//go:embed sql/schema.sql
var createTables string

// TODO:
// Complete the database schema by adding extra things like names and shit
// Make endpoints to create basic objects
// Make simple admin panel
// Extract db path to env var
// Extract port to env var
func run() error {
	port := 8080

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}

	if _, err := db.Exec(createTables); err != nil {
		return err
	}

	queries := dbutils.New(db)

	http.HandleFunc("/laundromat/create", func(w http.ResponseWriter, r *http.Request) {
		queries.CreateLaundromat(r.Context())
		w.WriteHeader(http.StatusCreated)
	})
	http.ListenAndServe(fmt.Sprintf(":%d", port), nil)

	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

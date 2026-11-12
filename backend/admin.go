package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"unicode/utf8"
)

type resource struct{ table, parent, parentTable string }

var resources = map[string]resource{
	"laundromats": {table: "laundromat"},
	"hostels":     {table: "hostel", parent: "laundromat_id", parentTable: "laundromat"},
	"students":    {table: "student_bag", parent: "hostel_id", parentTable: "hostel"},
	"handlers":    {table: "handler"},
}

type record struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	LaundromatID *int64 `json:"laundromat_id,omitempty"`
	HostelID     *int64 `json:"hostel_id,omitempty"`
}

type input struct {
	RollNumber   *int64 `json:"roll_number"`
	Name         string `json:"name"`
	LaundromatID *int64 `json:"laundromat_id"`
	HostelID     *int64 `json:"hostel_id"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if value != nil {
		_ = json.NewEncoder(w).Encode(value)
	}
}
func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
func dbError(w http.ResponseWriter, err error) {
	log.Printf("database: %v", err)
	fail(w, 500, "Database operation failed")
}
func newHandler(db *sql.DB) http.Handler {
	mux := http.NewServeMux()
	hub := registerWorkflow(mux, db)
	auth := registerAuth(mux, db)
	for name, spec := range resources {
		base := "/api/" + name
		mux.HandleFunc("GET "+base, func(w http.ResponseWriter, r *http.Request) { listRecords(db, spec, w, r) })
		mux.HandleFunc("POST "+base+"/bulk", func(w http.ResponseWriter, r *http.Request) { bulkRecords(db, spec, base, w, r) })
		mux.HandleFunc("POST "+base, func(w http.ResponseWriter, r *http.Request) { saveRecord(db, spec, base, w, r, false) })
		mux.HandleFunc("PUT "+base+"/{id}", func(w http.ResponseWriter, r *http.Request) { saveRecord(db, spec, base, w, r, true) })
		mux.HandleFunc("DELETE "+base+"/{id}", func(w http.ResponseWriter, r *http.Request) { deleteRecord(db, spec, w, r) })
	}
	// Preserve the original create URL, now with the same required JSON body.
	mux.HandleFunc("POST /laundromat/create", func(w http.ResponseWriter, r *http.Request) {
		saveRecord(db, resources["laundromats"], "/api/laundromats", w, r, false)
	})
	registerFrontends(mux)
	return auth.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r)
		if r.Method != "GET" && r.Method != "HEAD" {
			hub.publish()
		}
	}))
}
func listRecords(db *sql.DB, s resource, w http.ResponseWriter, r *http.Request) {
	columns := "id, COALESCE(name, '')"
	if s.parent != "" {
		columns += ", " + s.parent
	}
	actor := current(r)
	query := "SELECT " + columns + " FROM " + s.table + " WHERE archived = 0"
	args := []any{}
	if actor.Role != "admin" {
		switch actor.Role {
		case "warden":
			if s.table == "hostel" {
				query += " AND id = ?"
				args = append(args, *actor.SubjectID)
			} else if s.table == "student_bag" {
				query += " AND hostel_id = ?"
				args = append(args, *actor.SubjectID)
			}
		case "laundromat":
			if s.table == "laundromat" {
				query += " AND id = ?"
				args = append(args, *actor.SubjectID)
			} else if s.table == "hostel" {
				query += " AND laundromat_id = ?"
				args = append(args, *actor.SubjectID)
			}
		case "handler", "student":
			query += " AND id = ?"
			args = append(args, *actor.SubjectID)
		}
	}
	rows, err := db.QueryContext(r.Context(), query+" ORDER BY id DESC", args...)
	if err != nil {
		dbError(w, err)
		return
	}
	defer rows.Close()
	result := []record{}
	for rows.Next() {
		var item record
		args := []any{&item.ID, &item.Name}
		if s.parent == "laundromat_id" {
			args = append(args, &item.LaundromatID)
		}
		if s.parent == "hostel_id" {
			args = append(args, &item.HostelID)
		}
		if err = rows.Scan(args...); err != nil {
			dbError(w, err)
			return
		}
		result = append(result, item)
	}
	if err = rows.Err(); err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, result)
}
func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, 400, "id must be a positive integer")
		return 0, false
	}
	return id, true
}
func saveRecord(db *sql.DB, s resource, base string, w http.ResponseWriter, r *http.Request, update bool) {
	var id int64
	if update {
		var ok bool
		id, ok = parseID(w, r)
		if !ok {
			return
		}
	}
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, 415, "Content-Type must be application/json")
		return
	}
	var body input
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		fail(w, 400, "Invalid JSON body")
		return
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		fail(w, 400, "Expected a single JSON object")
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || utf8.RuneCountInString(body.Name) > 120 {
		fail(w, 400, "name must contain 1 to 120 characters")
		return
	}
	if (s.parent != "laundromat_id" && body.LaundromatID != nil) || (s.parent != "hostel_id" && body.HostelID != nil) {
		fail(w, 400, "Unexpected parent field for this resource")
		return
	}
	var parent *int64
	if s.parent == "laundromat_id" {
		parent = body.LaundromatID
	}
	if s.parent == "hostel_id" {
		parent = body.HostelID
	}
	if actor := current(r); actor.Role == "warden" {
		if s.table != "student_bag" || parent == nil || *parent != *actor.SubjectID {
			fail(w, 403, "You can register students only for your hostel")
			return
		}
	}
	if parent != nil && *parent <= 0 {
		fail(w, 400, s.parent+" must be a positive integer")
		return
	}
	if s.table == "student_bag" {
		if body.RollNumber == nil || *body.RollNumber <= 0 || *body.RollNumber > 9007199254740991 {
			fail(w, 400, "roll_number must be a positive safe integer")
			return
		}
		if update && *body.RollNumber != id {
			fail(w, 400, "Student roll numbers cannot be changed")
			return
		}
	} else if body.RollNumber != nil {
		fail(w, 400, "roll_number is only valid for students")
		return
	}
	tx, shared := r.Context().Value(bulkTxKey{}).(*sql.Tx)
	var err error
	if !shared {
		tx, err = db.BeginTx(r.Context(), nil)
	}
	if err != nil {
		dbError(w, err)
		return
	}
	if !shared {
		defer tx.Rollback()
	}
	if update {
		var exists int
		err = tx.QueryRowContext(r.Context(), "SELECT 1 FROM "+s.table+" WHERE id = ? AND archived = 0", id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "Record not found")
			return
		}
		if err != nil {
			dbError(w, err)
			return
		}
	}
	if parent != nil {
		var exists int
		err = tx.QueryRowContext(r.Context(), "SELECT 1 FROM "+s.parentTable+" WHERE id = ? AND archived = 0", *parent).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 400, s.parent+" does not reference an existing record")
			return
		}
		if err != nil {
			dbError(w, err)
			return
		}
	}
	args := []any{body.Name}
	statement := "INSERT INTO " + s.table + " (name) VALUES (?)"
	if parent != nil {
		args = append(args, *parent)
		statement = "INSERT INTO " + s.table + " (name, " + s.parent + ") VALUES (?, ?)"
	}
	if !update && s.table == "student_bag" {
		var exists int
		err = tx.QueryRowContext(r.Context(), "SELECT 1 FROM student_bag WHERE id = ?", *body.RollNumber).Scan(&exists)
		if err == nil {
			fail(w, 409, "Roll number already exists, including removed students")
			return
		}
		if !errors.Is(err, sql.ErrNoRows) {
			dbError(w, err)
			return
		}
		statement = "INSERT INTO student_bag (name, hostel_id, id) VALUES (?, ?, ?)"
		args = []any{body.Name, parent, *body.RollNumber}
	}
	if update {
		args = []any{body.Name}
		statement = "UPDATE " + s.table + " SET name = ?"
		if s.parent != "" {
			statement += ", " + s.parent + " = ?"
			args = append(args, parent)
		}
		statement += " WHERE id = ?"
		args = append(args, id)
	}
	res, err := tx.ExecContext(r.Context(), statement, args...)
	if err != nil {
		dbError(w, err)
		return
	}
	if !update {
		id, err = res.LastInsertId()
		if err != nil {
			dbError(w, err)
			return
		}
	}
	if !shared {
		err = tx.Commit()
	}
	if err != nil {
		dbError(w, err)
		return
	}
	status := http.StatusOK
	if !update {
		status = http.StatusCreated
		w.Header().Set("Location", fmt.Sprintf("%s/%d", base, id))
	}
	writeJSON(w, status, record{ID: id, Name: body.Name, LaundromatID: body.LaundromatID, HostelID: body.HostelID})
}
func deleteRecord(db *sql.DB, s resource, w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		dbError(w, err)
		return
	}
	defer tx.Rollback()
	if actor := current(r); actor.Role == "warden" {
		var hostel sql.NullInt64
		err = tx.QueryRowContext(r.Context(), "SELECT hostel_id FROM student_bag WHERE id=? AND archived=0", id).Scan(&hostel)
		if errors.Is(err, sql.ErrNoRows) {
			fail(w, 404, "Record not found")
			return
		}
		if err != nil {
			dbError(w, err)
			return
		}
		if !hostel.Valid || hostel.Int64 != *actor.SubjectID {
			fail(w, 403, "Student belongs to another hostel")
			return
		}
	}
	// Retain identities and history; detach active children from removed parents.
	children := map[string]string{"laundromat": "UPDATE hostel SET laundromat_id = NULL WHERE laundromat_id = ?", "hostel": "UPDATE student_bag SET hostel_id = NULL WHERE hostel_id = ?"}
	if query := children[s.table]; query != "" {
		if _, err = tx.ExecContext(r.Context(), query, id); err != nil {
			dbError(w, err)
			return
		}
	}
	result, err := tx.ExecContext(r.Context(), "UPDATE "+s.table+" SET archived = 1 WHERE id = ? AND archived = 0", id)
	if err != nil {
		dbError(w, err)
		return
	}
	count, err := result.RowsAffected()
	if err != nil {
		dbError(w, err)
		return
	}
	if count == 0 {
		fail(w, 404, "Record not found")
		return
	}
	if err = tx.Commit(); err != nil {
		dbError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type bulkTxKey struct{}

// A batch uses exactly the single-record validation and one all-or-nothing transaction.
func bulkRecords(db *sql.DB, s resource, base string, w http.ResponseWriter, r *http.Request) {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, 415, "Content-Type must be application/json")
		return
	}
	var items []json.RawMessage
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&items); err != nil {
		fail(w, 400, "Expected a JSON array")
		return
	}
	if dec.Decode(new(any)) != io.EOF || len(items) == 0 || len(items) > 1000 {
		fail(w, 400, "Provide 1 to 1000 records in a single array")
		return
	}
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		dbError(w, err)
		return
	}
	defer tx.Rollback()
	result := []record{}
	for i, item := range items {
		req := httptest.NewRequest("POST", base, bytes.NewReader(item)).WithContext(context.WithValue(r.Context(), bulkTxKey{}, tx))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		saveRecord(db, s, base, response, req, false)
		if response.Code != http.StatusCreated {
			var message map[string]string
			_ = json.Unmarshal(response.Body.Bytes(), &message)
			fail(w, response.Code, fmt.Sprintf("Record %d: %s", i+1, message["error"]))
			return
		}
		var saved record
		_ = json.Unmarshal(response.Body.Bytes(), &saved)
		result = append(result, saved)
	}
	if err = tx.Commit(); err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

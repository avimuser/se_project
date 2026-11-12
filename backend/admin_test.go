package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecordsAndPortals(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA foreign_keys=ON;" + createTables); err != nil {
		t.Fatal(err)
	}
	h := newHandler(db)
	auth := authAdmin(t, db, h)
	request := func(method, path, body string, status int) string {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.AddCookie(auth.Cookie)
		r.Header.Set("X-CSRF-Token", auth.CSRF)
		h.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("%s %s: got %d: %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	request("POST", "/api/laundromats", `{"name":"Laundry"}`, 201)
	request("POST", "/api/hostels", `{"name":"Hostel","laundromat_id":1}`, 201)
	request("POST", "/api/students", `{"name":"A","roll_number":2026001,"hostel_id":1}`, 201)
	request("POST", "/api/students", `{"name":"B","roll_number":2026001,"hostel_id":1}`, 409)
	request("POST", "/api/students/bulk", `[{"name":"B","roll_number":2026002,"hostel_id":1},{"name":"C","roll_number":2026001,"hostel_id":1}]`, 409)
	var count int
	db.QueryRow("SELECT count(*) FROM student_bag").Scan(&count)
	if count != 1 {
		t.Fatal("batch was not rolled back")
	}
	request("POST", "/api/students/bulk", `[{"name":"B","roll_number":2026002,"hostel_id":1}]`, 201)
	request("PUT", "/api/students/2026002", `{"name":"B updated","roll_number":2026002,"hostel_id":null}`, 200)
	request("PUT", "/api/students/2026002", `{"name":"B updated","roll_number":2026002,"hostel_id":1}`, 200)
	request("DELETE", "/api/laundromats/1", "", 204)
	var parent sql.NullInt64
	db.QueryRow("SELECT laundromat_id FROM hostel WHERE id=1").Scan(&parent)
	if parent.Valid {
		t.Fatal("hostel not orphaned")
	}
	request("DELETE", "/api/hostels/1", "", 204)
	db.QueryRow("SELECT hostel_id FROM student_bag WHERE id=2026001").Scan(&parent)
	if parent.Valid {
		t.Fatal("student not orphaned")
	}
	request("DELETE", "/api/students/2026001", "", 204)
	db.QueryRow("SELECT count(*) FROM student_bag WHERE id=2026001 AND archived=1").Scan(&count)
	if count != 1 {
		t.Fatal("identity not retained")
	}
	request("DELETE", "/api/students/2026001", "", 404)
	request("POST", "/api/students", `{"name":"A","roll_number":2026001}`, 409)
	request("POST", "/api/students", `{"name":"A"}`, 400)
	request("GET", "/api/missing", "", 404)
	request("GET", "/", "", http.StatusSeeOther)
	for _, path := range []string{"/admin/", "/warden/", "/laundromat/", "/handler/", "/student/"} {
		if body := request("GET", path, "", 200); !strings.Contains(body, `id="root"`) {
			t.Fatal("missing frontend")
		}
	}
}

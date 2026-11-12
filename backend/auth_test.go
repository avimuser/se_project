package main

import (
	"database/sql"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestAccessBoundaries(t *testing.T) {
	f := setupWorkflow(t)
	raw := httptest.NewRecorder()
	f.h.ServeHTTP(raw, httptest.NewRequest("GET", "/api/workflow", nil))
	if raw.Code != 401 {
		t.Fatalf("anonymous workflow: %d", raw.Code)
	}
	legacy := httptest.NewRecorder()
	f.h.ServeHTTP(legacy, httptest.NewRequest("POST", "/laundromat/create", strings.NewReader(`{"name":"Unauthorized"}`)))
	if legacy.Code != 401 {
		t.Fatalf("legacy endpoint unauthenticated: %d", legacy.Code)
	}
	if w := authedRequest(f.h, f.auth, "GET", "/api/auth/me", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"role":"admin"`) {
		t.Fatalf("auth me: %d %s", w.Code, w.Body.String())
	}
	if w := authedRequest(f.h, f.studentAuth[101], "GET", "/api/students", ""); w.Code != 200 || strings.Contains(w.Body.String(), `"id":102`) {
		t.Fatalf("student directory leaked: %d %s", w.Code, w.Body.String())
	}
	if w := authedRequest(f.h, f.studentAuth[101], "GET", "/api/workflow?student_id=102", ""); w.Code != 403 {
		t.Fatalf("other student's history: %d", w.Code)
	}
	if w := authedRequest(f.h, f.studentAuth[101], "POST", "/api/notifications/read", `{"student_id":102}`); w.Code != 403 {
		t.Fatalf("other student's notifications: %d", w.Code)
	}
	if w := authedRequest(f.h, f.handlerAuth, "GET", "/api/students", ""); w.Code != 403 {
		t.Fatalf("handler directory: %d", w.Code)
	}
	if w := authedRequest(f.h, f.handlerAuth, "POST", "/api/jobs", `{}`); w.Code != 403 {
		t.Fatalf("handler schedule: %d", w.Code)
	}
	if w := authedRequest(f.h, f.laundryAuth, "POST", "/api/jobs", `{"kind":"pickup","laundromat_id":2}`); w.Code != 403 {
		t.Fatalf("other laundromat: %d", w.Code)
	}
	if w := authedRequest(f.h, f.laundryAuth, "GET", "/api/laundromats", ""); w.Code != 200 || strings.Contains(w.Body.String(), `"id":2`) {
		t.Fatalf("laundromat directory leaked: %d %s", w.Code, w.Body.String())
	}
	req := httptest.NewRequest("POST", "/api/students", strings.NewReader(`{"name":"X"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(f.auth.Cookie)
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("missing CSRF: %d", w.Code)
	}
	req = httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"long-test-password-123"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	w = httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("cross-origin login: %d", w.Code)
	}
	if _, err := createUser(t.Context(), f.db, "warden", "warden-password-123", "warden", ptr(1)); err != nil {
		t.Fatal(err)
	}
	warden := signIn(t, f.h, "warden", "warden-password-123")
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/api/students", `{"name":"Other","roll_number":104,"hostel_id":2}`, 403},
		{"/api/students", `{"name":"Own","roll_number":104,"hostel_id":1}`, 201},
		{"/api/students/bulk", `[{"name":"Other","roll_number":105,"hostel_id":2}]`, 403},
		{"/api/accounts", `{"username":"outsider","password":"long-password-123","role":"student","subject_id":103}`, 403},
		{"/api/accounts", `{"username":"ownstudent","password":"long-password-123","role":"student","subject_id":104}`, 201},
	} {
		w := authedRequest(f.h, warden, "POST", tc.path, tc.body)
		if w.Code != tc.status {
			t.Fatalf("warden POST %s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	if w := authedRequest(f.h, warden, "GET", "/api/students", ""); w.Code != 200 || strings.Contains(w.Body.String(), `"id":103`) {
		t.Fatalf("warden students leaked: %d %s", w.Code, w.Body.String())
	}
	if w := authedRequest(f.h, warden, "DELETE", "/api/students/103", ""); w.Code != 403 {
		t.Fatalf("warden removed another hostel's student: %d", w.Code)
	}
	if w := authedRequest(f.h, warden, "GET", "/api/accounts", ""); w.Code != 200 || strings.Contains(w.Body.String(), `"username":"admin"`) {
		t.Fatalf("warden accounts leaked: %d %s", w.Code, w.Body.String())
	}
	var accountID int64
	if err := f.db.QueryRow("SELECT id FROM auth_user WHERE username='ownstudent'").Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if w := authedRequest(f.h, warden, "PUT", "/api/accounts/1", `{"password":"other-password-123"}`); w.Code != 403 {
		t.Fatalf("warden reset admin: %d", w.Code)
	}
	if w := authedRequest(f.h, warden, "DELETE", "/api/accounts/1", ""); w.Code != 403 {
		t.Fatalf("warden revoked admin: %d", w.Code)
	}
	if w := authedRequest(f.h, warden, "PUT", "/api/accounts/"+strconv.FormatInt(accountID, 10), `{"password":"new-password-123"}`); w.Code != 200 {
		t.Fatalf("warden reset own student: %d %s", w.Code, w.Body.String())
	}
	student := signIn(t, f.h, "ownstudent", "new-password-123")
	if w := authedRequest(f.h, warden, "DELETE", "/api/accounts/"+strconv.FormatInt(accountID, 10), ""); w.Code != 204 {
		t.Fatalf("warden revoke own student: %d", w.Code)
	}
	if w := authedRequest(f.h, student, "GET", "/api/auth/me", ""); w.Code != 401 {
		t.Fatalf("revoked session: %d", w.Code)
	}
}
func ptr(id int64) *int64 { return &id }
func TestBootstrapRequiresCredential(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(createTables); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADMIN_PASSWORD", "")
	if err = bootstrapAdmin(db); err == nil {
		t.Fatal("empty initial admin password accepted")
	}
	t.Setenv("ADMIN_PASSWORD", "initial-password-123")
	if err = bootstrapAdmin(db); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ADMIN_PASSWORD", "")
	if err = bootstrapAdmin(db); err != nil {
		t.Fatal("existing admin should start without environment password:", err)
	}
}

func TestPasswordChangeAndLogout(t *testing.T) {
	f := setupWorkflow(t)
	if w := authedRequest(f.h, f.auth, "POST", "/api/auth/password", `{"old_password":"wrong-password","new_password":"new-admin-password-123"}`); w.Code != 403 {
		t.Fatalf("incorrect old password: %d", w.Code)
	}
	if w := authedRequest(f.h, f.auth, "POST", "/api/auth/password", `{"old_password":"long-test-password-123","new_password":"new-admin-password-123"}`); w.Code != 200 {
		t.Fatalf("change password: %d %s", w.Code, w.Body.String())
	}
	raw := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"long-test-password-123"}`))
	raw.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, raw)
	if w.Code != 401 {
		t.Fatalf("old password accepted: %d", w.Code)
	}
	fresh := signIn(t, f.h, "admin", "new-admin-password-123")
	if w := authedRequest(f.h, fresh, "POST", "/api/auth/logout", ""); w.Code != 204 {
		t.Fatalf("logout: %d", w.Code)
	}
	if w := authedRequest(f.h, fresh, "GET", "/api/auth/me", ""); w.Code != 401 {
		t.Fatalf("logged-out session still active: %d", w.Code)
	}
}

package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testSession struct {
	Cookie *http.Cookie
	CSRF   string
}

func authAdmin(t *testing.T, db *sql.DB, h http.Handler) testSession {
	t.Helper()
	if _, err := createUser(context.Background(), db, "admin", "long-test-password-123", "admin", nil); err != nil {
		t.Fatal(err)
	}
	return signIn(t, h, "admin", "long-test-password-123")
}
func signIn(t *testing.T, h http.Handler, username, password string) testSession {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("login %s: %d %s", username, w.Code, w.Body.String())
	}
	var result session
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("missing session cookie")
	}
	return testSession{cookies[0], result.CSRF}
}
func authedRequest(h http.Handler, s testSession, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-CSRF-Token", s.CSRF)
	r.AddCookie(s.Cookie)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

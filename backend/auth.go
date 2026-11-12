package main

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

const passwordRounds = 600000
const sessionLifetime = 7 * 24 * time.Hour
const sessionCookie = "laundry_session"

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{3,64}$`)

type account struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	SubjectID *int64 `json:"subject_id"`
	Active    bool   `json:"active"`
}
type session struct {
	account
	CSRF string `json:"csrf"`
}
type accountKey struct{}

func current(r *http.Request) session { v, _ := r.Context().Value(accountKey{}).(session); return v }
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
func hashPassword(password string, salt []byte) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, passwordRounds, 32)
}
func passwordValid(password string) bool { return len(password) >= 12 && len(password) <= 128 }
func createUser(ctx context.Context, db *sql.DB, username, password, role string, subject *int64) (int64, error) {
	username = strings.TrimSpace(username)
	if !usernamePattern.MatchString(username) || !passwordValid(password) {
		return 0, errors.New("Username must be 3–64 letters, digits, dots, underscores or hyphens; password must be 12–128 characters")
	}
	if role != "admin" && role != "warden" && role != "laundromat" && role != "handler" && role != "student" {
		return 0, errors.New("Invalid role")
	}
	if (role == "admin") != (subject == nil) {
		return 0, errors.New("Select a record for this role")
	}
	if subject != nil {
		if *subject <= 0 {
			return 0, errors.New("Invalid record")
		}
		table := map[string]string{"warden": "hostel", "laundromat": "laundromat", "handler": "handler", "student": "student_bag"}[role]
		var exists int
		err := db.QueryRowContext(ctx, "SELECT 1 FROM "+table+" WHERE id=? AND archived=0", *subject).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, errors.New("The selected record is missing or removed")
		}
		if err != nil {
			return 0, err
		}
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return 0, err
	}
	hash, err := hashPassword(password, salt)
	if err != nil {
		return 0, err
	}
	res, err := db.ExecContext(ctx, "INSERT INTO auth_user(username,salt,password_hash,role,subject_id) VALUES(?,?,?,?,?)", username, salt, hash, role, subject)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
func bootstrapAdmin(db *sql.DB) error {
	var count int
	if err := db.QueryRow("SELECT count(*) FROM auth_user WHERE role='admin' AND active=1").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	username := os.Getenv("ADMIN_USERNAME")
	if username == "" {
		username = "admin"
	}
	password := os.Getenv("ADMIN_PASSWORD")
	if password == "" {
		return errors.New("set ADMIN_PASSWORD (at least 12 characters) to create the initial administrator")
	}
	_, err := createUser(context.Background(), db, username, password, "admin", nil)
	return err
}

type limiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	old := l.attempts[key]
	fresh := old[:0]
	for _, t := range old {
		if now.Sub(t) < time.Minute {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) >= 10 {
		l.attempts[key] = fresh
		return false
	}
	l.attempts[key] = append(fresh, now)
	return true
}
func (l *limiter) clear(key string) { l.mu.Lock(); delete(l.attempts, key); l.mu.Unlock() }

type authAPI struct {
	db      *sql.DB
	limiter limiter
}

func registerAuth(mux *http.ServeMux, db *sql.DB) *authAPI {
	a := &authAPI{db: db, limiter: limiter{attempts: map[string][]time.Time{}}}
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("POST /api/auth/logout", a.logout)
	mux.HandleFunc("GET /api/auth/me", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, current(r)) })
	mux.HandleFunc("POST /api/auth/password", a.changePassword)
	mux.HandleFunc("GET /api/accounts", a.listAccounts)
	mux.HandleFunc("POST /api/accounts", a.addAccount)
	mux.HandleFunc("PUT /api/accounts/{id}", a.updateAccount)
	mux.HandleFunc("DELETE /api/accounts/{id}", a.deleteAccount)
	return a
}
func allowedOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || origin == "http://"+r.Host || origin == "https://"+r.Host
}
func (a *authAPI) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/laundromat/create" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if !allowedOrigin(r) {
			fail(w, 403, "Origin not allowed")
			return
		}
		if r.URL.Path == "/api/auth/login" {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || len(cookie.Value) != 64 {
			fail(w, 401, "Sign in required")
			return
		}
		token, err := hex.DecodeString(cookie.Value)
		if err != nil {
			fail(w, 401, "Sign in required")
			return
		}
		digest := sha256.Sum256(token)
		var s session
		var subject sql.NullInt64
		var expiry int64
		err = a.db.QueryRowContext(r.Context(), `SELECT u.id,u.username,u.role,u.subject_id,u.active,s.csrf,s.expires_at FROM auth_session s JOIN auth_user u ON u.id=s.user_id WHERE s.token_hash=?`, digest[:]).Scan(&s.ID, &s.Username, &s.Role, &subject, &s.Active, &s.CSRF, &expiry)
		if err != nil || !s.Active || expiry < time.Now().Unix() {
			fail(w, 401, "Sign in required")
			return
		}
		if subject.Valid {
			s.SubjectID = &subject.Int64
		}
		if s.Role != "admin" {
			table := map[string]string{"warden": "hostel", "laundromat": "laundromat", "handler": "handler", "student": "student_bag"}[s.Role]
			if table == "" || s.SubjectID == nil {
				fail(w, 401, "Account is no longer available")
				return
			}
			var exists int
			err = a.db.QueryRowContext(r.Context(), "SELECT 1 FROM "+table+" WHERE id=? AND archived=0", *s.SubjectID).Scan(&exists)
			if err != nil {
				fail(w, 401, "Account is no longer available")
				return
			}
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("X-CSRF-Token") != s.CSRF {
			fail(w, 403, "Invalid CSRF token")
			return
		}
		if !authorized(s, r) {
			fail(w, 403, "You do not have access to this resource")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), accountKey{}, s)))
	})
}
func authorized(s session, r *http.Request) bool {
	p := r.URL.Path
	m := r.Method
	if strings.HasPrefix(p, "/api/auth/") {
		return true
	}
	if strings.HasPrefix(p, "/api/accounts") {
		return s.Role == "admin" || s.Role == "warden"
	}
	if p == "/api/events" {
		return true
	}
	if strings.HasPrefix(p, "/api/jobs") {
		return (s.Role == "laundromat" && m == "POST" && p == "/api/jobs") || (s.Role == "handler" && m == "POST" && p != "/api/jobs")
	}
	if p == "/api/workflow" {
		return s.Role == "admin" || s.Role == "laundromat" || s.Role == "handler" || s.Role == "student"
	}
	if p == "/api/notifications/read" {
		return s.Role == "student" && m == "POST"
	}
	if p == "/laundromat/create" {
		return s.Role == "admin"
	}
	for name := range resources {
		base := "/api/" + name
		if p == base || strings.HasPrefix(p, base+"/") {
			if s.Role == "admin" {
				return true
			}
			if s.Role == "warden" {
				return (m == "GET" && (name == "hostels" || name == "students")) || (name == "students" && (m == "POST" || m == "DELETE"))
			}
			if m != "GET" || p != base {
				return false
			}
			if s.Role == "laundromat" {
				return name == "laundromats" || name == "hostels" || name == "handlers"
			}
			if s.Role == "handler" {
				return name == "handlers"
			}
			if s.Role == "student" {
				return name == "students"
			}
		}
	}
	return s.Role == "admin"
}
func (a *authAPI) login(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &b) {
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	key := ip + ":" + strings.ToLower(strings.TrimSpace(b.Username))
	if !a.limiter.allow(key) {
		fail(w, 429, "Too many sign-in attempts. Try again in a minute.")
		return
	}
	var user account
	var salt, stored []byte
	err = a.db.QueryRowContext(r.Context(), "SELECT id,username,role,subject_id,active,salt,password_hash FROM auth_user WHERE username=?", b.Username).Scan(&user.ID, &user.Username, &user.Role, &user.SubjectID, &user.Active, &salt, &stored)
	if err != nil { // Do comparable work for unknown usernames.
		salt = make([]byte, 16)
		stored = make([]byte, 32)
	}
	candidate, e := hashPassword(b.Password, salt)
	if e != nil {
		dbError(w, e)
		return
	}
	if err != nil || !user.Active || subtle.ConstantTimeCompare(candidate, stored) != 1 {
		fail(w, 401, "Invalid username or password")
		return
	}
	if user.Role != "admin" {
		table := map[string]string{"warden": "hostel", "laundromat": "laundromat", "handler": "handler", "student": "student_bag"}[user.Role]
		var exists int
		if user.SubjectID == nil || a.db.QueryRowContext(r.Context(), "SELECT 1 FROM "+table+" WHERE id=? AND archived=0", *user.SubjectID).Scan(&exists) != nil {
			fail(w, 401, "Account is no longer available")
			return
		}
	}
	raw, err := randomHex(32)
	if err != nil {
		dbError(w, err)
		return
	}
	token, _ := hex.DecodeString(raw)
	digest := sha256.Sum256(token)
	csrf, err := randomHex(32)
	if err != nil {
		dbError(w, err)
		return
	}
	expiry := time.Now().Add(sessionLifetime)
	if _, err = a.db.ExecContext(r.Context(), "DELETE FROM auth_session WHERE expires_at < ?", time.Now().Unix()); err != nil {
		dbError(w, err)
		return
	}
	if _, err = a.db.ExecContext(r.Context(), "INSERT INTO auth_session(token_hash,user_id,csrf,expires_at) VALUES(?,?,?,?)", digest[:], user.ID, csrf, expiry.Unix()); err != nil {
		dbError(w, err)
		return
	}
	secure := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: raw, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, Expires: expiry})
	a.limiter.clear(key)
	writeJSON(w, 200, session{account: user, CSRF: csrf})
}
func (a *authAPI) logout(w http.ResponseWriter, r *http.Request) {
	cookie, _ := r.Cookie(sessionCookie)
	token, _ := hex.DecodeString(cookie.Value)
	digest := sha256.Sum256(token)
	if _, err := a.db.ExecContext(r.Context(), "DELETE FROM auth_session WHERE token_hash=?", digest[:]); err != nil {
		dbError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(204)
}
func (a *authAPI) changePassword(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Old string `json:"old_password"`
		New string `json:"new_password"`
	}
	if !decodeBody(w, r, &b) {
		return
	}
	if !passwordValid(b.New) {
		fail(w, 400, "Password must be 12–128 characters")
		return
	}
	s := current(r)
	var salt, stored []byte
	err := a.db.QueryRowContext(r.Context(), "SELECT salt,password_hash FROM auth_user WHERE id=?", s.ID).Scan(&salt, &stored)
	if err != nil {
		dbError(w, err)
		return
	}
	candidate, _ := hashPassword(b.Old, salt)
	if subtle.ConstantTimeCompare(candidate, stored) != 1 {
		fail(w, 403, "Current password is incorrect")
		return
	}
	a.setPassword(w, r, s.ID, b.New, true)
}
func (a *authAPI) setPassword(w http.ResponseWriter, r *http.Request, id int64, password string, revokeOthers bool) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		dbError(w, err)
		return
	}
	hash, err := hashPassword(password, salt)
	if err != nil {
		dbError(w, err)
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		dbError(w, err)
		return
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(r.Context(), "UPDATE auth_user SET salt=?,password_hash=? WHERE id=?", salt, hash, id)
	if err != nil {
		dbError(w, err)
		return
	}
	count, _ := res.RowsAffected()
	if count == 0 {
		fail(w, 404, "Account not found")
		return
	}
	if revokeOthers {
		_, err = tx.ExecContext(r.Context(), "DELETE FROM auth_session WHERE user_id=? AND token_hash<>?", id, sessionDigest(r))
	} else {
		_, err = tx.ExecContext(r.Context(), "DELETE FROM auth_session WHERE user_id=?", id)
	}
	if err != nil {
		dbError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func sessionDigest(r *http.Request) []byte {
	c, _ := r.Cookie(sessionCookie)
	token, _ := hex.DecodeString(c.Value)
	d := sha256.Sum256(token)
	return d[:]
}
func (a *authAPI) listAccounts(w http.ResponseWriter, r *http.Request) {
	query := "SELECT id,username,role,subject_id,active FROM auth_user"
	args := []any{}
	if actor := current(r); actor.Role == "warden" {
		query += " WHERE role='student' AND subject_id IN (SELECT id FROM student_bag WHERE hostel_id=? AND archived=0)"
		args = append(args, *actor.SubjectID)
	}
	rows, err := a.db.QueryContext(r.Context(), query+" ORDER BY id DESC", args...)
	if err != nil {
		dbError(w, err)
		return
	}
	defer rows.Close()
	out := []account{}
	for rows.Next() {
		var v account
		if err = rows.Scan(&v.ID, &v.Username, &v.Role, &v.SubjectID, &v.Active); err != nil {
			dbError(w, err)
			return
		}
		out = append(out, v)
	}
	if err = rows.Err(); err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (a *authAPI) addAccount(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Username  string `json:"username"`
		Password  string `json:"password"`
		Role      string `json:"role"`
		SubjectID *int64 `json:"subject_id"`
	}
	if !decodeBody(w, r, &b) {
		return
	}
	if actor := current(r); actor.Role == "warden" {
		if b.Role != "student" || b.SubjectID == nil || !a.wardenOwns(r, *actor.SubjectID, *b.SubjectID) {
			fail(w, 403, "You can create accounts only for your students")
			return
		}
	}
	id, err := createUser(r.Context(), a.db, b.Username, b.Password, b.Role, b.SubjectID)
	if err != nil {
		if strings.Contains(err.Error(), "constraint failed") {
			fail(w, 409, "Username or role assignment already has an account")
		} else {
			fail(w, 400, err.Error())
		}
		return
	}
	writeJSON(w, 201, map[string]int64{"id": id})
}
func (a *authAPI) updateAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var b struct {
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &b) {
		return
	}
	if !passwordValid(b.Password) {
		fail(w, 400, "Password must be 12–128 characters")
		return
	}
	if !a.canManage(r, id) {
		fail(w, 403, "You cannot manage this account")
		return
	}
	a.setPassword(w, r, id, b.Password, false)
}
func (a *authAPI) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	s := current(r)
	if id == s.ID {
		fail(w, 409, "You cannot deactivate your own account")
		return
	}
	if !a.canManage(r, id) {
		fail(w, 403, "You cannot manage this account")
		return
	}
	res, err := a.db.ExecContext(r.Context(), "UPDATE auth_user SET active=0 WHERE id=? AND active=1", id)
	if err != nil {
		dbError(w, err)
		return
	}
	count, _ := res.RowsAffected()
	if count == 0 {
		fail(w, 404, "Account not found")
		return
	}
	a.db.ExecContext(r.Context(), "DELETE FROM auth_session WHERE user_id=?", id)
	w.WriteHeader(204)
}

func (a *authAPI) wardenOwns(r *http.Request, hostel, student int64) bool {
	var exists int
	return a.db.QueryRowContext(r.Context(), "SELECT 1 FROM student_bag WHERE id=? AND hostel_id=? AND archived=0", student, hostel).Scan(&exists) == nil
}
func (a *authAPI) canManage(r *http.Request, id int64) bool {
	actor := current(r)
	if actor.Role == "admin" {
		return true
	}
	if actor.Role != "warden" {
		return false
	}
	var exists int
	return a.db.QueryRowContext(r.Context(), `SELECT 1 FROM auth_user u JOIN student_bag s ON s.id=u.subject_id WHERE u.id=? AND u.role='student' AND s.hostel_id=? AND s.archived=0`, id, *actor.SubjectID).Scan(&exists) == nil
}

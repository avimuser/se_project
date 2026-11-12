package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	t           *testing.T
	db          *sql.DB
	h           http.Handler
	auth        testSession
	laundryAuth testSession
	handlerAuth testSession
	studentAuth map[int64]testSession
}

func setupWorkflow(t *testing.T) *fixture {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err = db.Exec("PRAGMA foreign_keys=ON;" + createTables); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, db: db, h: newHandler(db)}
	f.auth = authAdmin(t, db, f.h)
	for _, entry := range []struct{ path, body string }{
		{"laundromats", `{"name":"Laundry"}`}, {"laundromats", `{"name":"Other Laundry"}`},
		{"hostels", `{"name":"Hostel","laundromat_id":1}`}, {"hostels", `{"name":"Other Hostel","laundromat_id":2}`},
		{"handlers", `{"name":"Handler"}`}, {"handlers", `{"name":"Other Handler"}`},
		{"students", `{"name":"A","roll_number":101,"hostel_id":1}`}, {"students", `{"name":"B","roll_number":102,"hostel_id":1}`}, {"students", `{"name":"C","roll_number":103,"hostel_id":2}`},
	} {
		f.request("POST", "/api/"+entry.path, entry.body, 201)
	}
	f.studentAuth = map[int64]testSession{}
	laundryID, handlerID := int64(1), int64(1)
	if _, err = createUser(t.Context(), db, "laundry", "laundry-password-123", "laundromat", &laundryID); err != nil {
		t.Fatal(err)
	}
	if _, err = createUser(t.Context(), db, "handler", "handler-password-123", "handler", &handlerID); err != nil {
		t.Fatal(err)
	}
	f.laundryAuth = signIn(t, f.h, "laundry", "laundry-password-123")
	f.handlerAuth = signIn(t, f.h, "handler", "handler-password-123")
	for _, id := range []int64{101, 102, 103} {
		name := fmt.Sprintf("student%d", id)
		password := "student-password-123"
		if _, err = createUser(t.Context(), db, name, password, "student", &id); err != nil {
			t.Fatal(err)
		}
		f.studentAuth[id] = signIn(t, f.h, name, password)
	}
	return f
}
func (f *fixture) request(method, path, body string, status int) string {
	f.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	auth := f.auth
	if path == "/api/jobs" || (path == "/api/workflow" && method == "GET") {
		auth = f.laundryAuth
	}
	if strings.HasPrefix(path, "/api/jobs/") {
		auth = f.handlerAuth
	}
	if strings.HasPrefix(path, "/api/workflow?student_id=") {
		var id int64
		fmt.Sscanf(path, "/api/workflow?student_id=%d", &id)
		if session, ok := f.studentAuth[id]; ok {
			var archived bool
			f.db.QueryRow("SELECT archived FROM student_bag WHERE id=?", id).Scan(&archived)
			if !archived {
				auth = session
			}
		}
	}
	if path == "/api/notifications/read" {
		auth = f.studentAuth[102]
	}
	r.AddCookie(auth.Cookie)
	r.Header.Set("X-CSRF-Token", auth.CSRF)
	f.h.ServeHTTP(w, r)
	if w.Code != status {
		f.t.Fatalf("%s %s expected %d got %d: %s", method, path, status, w.Code, w.Body.String())
	}
	return w.Body.String()
}
func (f *fixture) schedule(kind string, students []int64, status int) int64 {
	f.t.Helper()
	body, _ := json.Marshal(map[string]any{"kind": kind, "students": students, "hostel_id": 1, "laundromat_id": 1, "handler_id": 1, "scheduled_time": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	out := f.request("POST", "/api/jobs", string(body), status)
	var result struct{ ID int64 }
	json.Unmarshal([]byte(out), &result)
	return result.ID
}
func (f *fixture) action(id int64, action string, student int64, failed bool, status int) {
	f.t.Helper()
	body := fmt.Sprintf(`{"handler_id":1,"student_id":%d,"failed":%t}`, student, failed)
	f.request("POST", fmt.Sprintf("/api/jobs/%d/%s", id, action), body, status)
}
func (f *fixture) state(student string) struct {
	Jobs          []job
	Bags          []bag
	Notifications []notification
} {
	f.t.Helper()
	var state struct {
		Jobs          []job
		Bags          []bag
		Notifications []notification
	}
	body := f.request("GET", "/api/workflow"+student, "", 200)
	if err := json.Unmarshal([]byte(body), &state); err != nil {
		f.t.Fatal(err)
	}
	return state
}
func TestFullLaundryCycleAndRetry(t *testing.T) {
	f := setupWorkflow(t)
	pickup := f.schedule("pickup", nil, 201)
	state := f.state("?student_id=101")
	if len(state.Notifications) != 1 || len(state.Jobs) != 1 {
		t.Fatal("scheduled pickup missing from student portal")
	}
	if len(f.state("?student_id=103").Notifications) != 0 {
		t.Fatal("notified wrong hostel")
	}
	f.action(pickup, "scan", 101, false, 409)
	f.request("POST", fmt.Sprintf("/api/jobs/%d/start", pickup), `{"handler_id":2}`, 403)
	f.action(pickup, "start", 0, false, 200)
	f.action(pickup, "start", 0, false, 409)
	f.action(pickup, "scan", 103, false, 400)
	f.action(pickup, "scan", 101, true, 400)
	f.action(pickup, "scan", 101, false, 200)
	f.action(pickup, "scan", 101, false, 409)
	f.action(pickup, "scan", 102, false, 200)
	f.schedule("delivery", []int64{101}, 409) // pickup must complete first
	f.action(pickup, "complete", 0, false, 200)
	f.action(pickup, "scan", 101, false, 409)
	f.schedule("delivery", []int64{101, 103}, 409) // entire reservation rolls back
	for _, b := range f.state("").Bags {
		if b.DeliveryJob != nil {
			t.Fatal("partial delivery reservation leaked")
		}
	}
	f.schedule("delivery", []int64{101, 101}, 400)
	delivery := f.schedule("delivery", []int64{101, 102}, 201)
	f.schedule("delivery", []int64{101}, 409)
	f.action(delivery, "start", 0, false, 200)
	f.action(delivery, "complete", 0, false, 409)
	f.action(delivery, "scan", 103, false, 409)
	f.action(delivery, "scan", 101, false, 200)
	f.action(delivery, "scan", 101, false, 409)
	f.action(delivery, "scan", 102, true, 200)
	f.schedule("delivery", []int64{102}, 409) // not yet returned
	f.action(delivery, "complete", 0, false, 200)
	state = f.state("")
	if len(state.Bags) != 1 || state.Bags[0].StudentID != 102 || state.Bags[0].DeliveryJob != nil {
		t.Fatal("failed delivery bag not returned to available custody")
	}
	retry := f.schedule("delivery", []int64{102}, 201)
	f.action(retry, "start", 0, false, 200)
	f.action(retry, "scan", 102, false, 200)
	f.action(retry, "complete", 0, false, 200)
	if len(f.state("").Bags) != 0 {
		t.Fatal("delivered bag remains in custody")
	}
	state = f.state("?student_id=102")
	if len(state.Jobs) != 3 || len(state.Notifications) < 5 {
		t.Fatal("incomplete student history/notifications")
	}
	foundFailure := false
	for _, j := range state.Jobs {
		for _, it := range j.Items {
			if it.Result == "failed" {
				foundFailure = true
			}
			if it.StudentID != 102 {
				t.Fatal("another student's history leaked into view")
			}
		}
	}
	if !foundFailure {
		t.Fatal("failed attempt lost from history")
	}
	f.request("POST", "/api/notifications/read", `{"student_id":102}`, 200)
	for _, n := range f.state("?student_id=102").Notifications {
		if !n.Read {
			t.Fatal("notification not marked read")
		}
	}
	// A fresh handler uses the same persisted state; history survives logical removal.
	f.request("DELETE", "/api/students/102", "", 204)
	f.h = newHandler(f.db)
	if len(f.state("?student_id=102").Jobs) != 3 {
		t.Fatal("history lost on removal or restart")
	}
	next := f.schedule("pickup", nil, 201)
	f.action(next, "start", 0, false, 200)
	f.action(next, "scan", 101, false, 200)
}
func TestWorkflowValidation(t *testing.T) {
	f := setupWorkflow(t)
	for _, body := range []string{`{"laundromat_id":1}`, `{"kind":"pickup","laundromat_id":1,"scheduled_time":"yesterday"}`, `{"kind":"other","laundromat_id":1}`, `{"unknown":1}`, `{} {}`} {
		f.request("POST", "/api/jobs", body, 400)
	}
	f.schedule("delivery", nil, 400)
	body := fmt.Sprintf(`{"kind":"pickup","hostel_id":2,"laundromat_id":1,"handler_id":1,"scheduled_time":%q}`, time.Now().Add(time.Hour).Format(time.RFC3339))
	f.request("POST", "/api/jobs", body, 400)
	f.request("POST", "/api/jobs/999/start", `{"handler_id":1}`, 404)
	f.request("POST", "/api/jobs/1/other", `{"handler_id":1}`, 404)
	f.request("GET", "/api/workflow?student_id=nope", "", 400)
}
func TestLiveStream(t *testing.T) {
	hub := &liveHub{clients: make(map[chan struct{}]bool)}
	server := httptest.NewServer(http.HandlerFunc(hub.stream))
	defer server.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("not an event stream")
	}
	reader := bufio.NewReader(response.Body)
	readEvent := func() {
		t.Helper()
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if line == "data: {}\n" {
				break
			}
		}
	}
	readEvent()
	hub.publish()
	readEvent()
	// Disconnect removes the subscriber.
	response.Body.Close()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		hub.mu.Lock()
		count := len(hub.clients)
		hub.mu.Unlock()
		if count == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("disconnected subscriber was not released")
}

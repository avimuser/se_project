package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type liveHub struct {
	mu      sync.Mutex
	clients map[chan struct{}]bool
}

func (h *liveHub) publish() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.clients {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
func (h *liveHub) stream(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "Streaming unavailable")
		return
	}
	// EventSource is same-origin; reject explicit cross-origin connections.
	if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host && origin != "https://"+r.Host {
		fail(w, 403, "Origin not allowed")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	ch := make(chan struct{}, 1)
	h.mu.Lock()
	h.clients[ch] = true
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.clients, ch); h.mu.Unlock() }()
	fmt.Fprint(w, "retry: 3000\nevent: change\ndata: {}\n\n")
	f.Flush()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ch:
			if _, err := fmt.Fprint(w, "event: change\ndata: {}\n\n"); err != nil {
				return
			}
			f.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			f.Flush()
		}
	}
}

type workflow struct {
	db  *sql.DB
	hub *liveHub
}

func registerWorkflow(mux *http.ServeMux, db *sql.DB) *liveHub {
	hub := &liveHub{clients: make(map[chan struct{}]bool)}
	a := &workflow{db, hub}
	mux.HandleFunc("GET /api/events", hub.stream)
	mux.HandleFunc("GET /api/workflow", a.snapshot)
	mux.HandleFunc("POST /api/jobs", a.schedule)
	mux.HandleFunc("POST /api/jobs/{id}/{action}", a.action)
	mux.HandleFunc("POST /api/notifications/read", a.markRead)
	return hub
}
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		fail(w, 415, "Content-Type must be application/json")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		fail(w, 400, "Invalid JSON body")
		return false
	}
	if d.Decode(new(any)) != io.EOF {
		fail(w, 400, "Expected a single JSON value")
		return false
	}
	return true
}

type job struct {
	ID            int64     `json:"id"`
	Kind          string    `json:"kind"`
	HostelID      int64     `json:"hostel_id"`
	LaundromatID  int64     `json:"laundromat_id"`
	HandlerID     int64     `json:"handler_id"`
	ScheduledTime string    `json:"scheduled_time"`
	Status        string    `json:"status"`
	Hostel        string    `json:"hostel"`
	Laundromat    string    `json:"laundromat"`
	Handler       string    `json:"handler"`
	Items         []jobItem `json:"items"`
}
type jobItem struct {
	StudentID int64   `json:"student_id"`
	Name      string  `json:"name"`
	Result    string  `json:"result"`
	ScannedAt *string `json:"scanned_at"`
}
type bag struct {
	StudentID    int64  `json:"student_id"`
	Name         string `json:"name"`
	HostelID     int64  `json:"hostel_id"`
	LaundromatID int64  `json:"laundromat_id"`
	PickupJob    int64  `json:"pickup_job"`
	DeliveryJob  *int64 `json:"delivery_job"`
	Archived     bool   `json:"archived"`
}
type notification struct {
	ID        int64  `json:"id"`
	StudentID int64  `json:"student_id"`
	JobID     int64  `json:"job_id"`
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
	Read      bool   `json:"read"`
}

func (a *workflow) snapshot(w http.ResponseWriter, r *http.Request) {
	// One read transaction gives portals a consistent view of jobs and custody.
	tx, err := a.db.BeginTx(r.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		dbError(w, err)
		return
	}
	defer tx.Rollback()
	jobs := []job{}
	bags := []bag{}
	notes := []notification{}
	rows, err := tx.QueryContext(r.Context(), `SELECT j.id,j.kind,j.hostel_id,j.laundromat_id,j.handler_id,j.scheduled_time,j.status,COALESCE(h.name,''),COALESCE(l.name,''),COALESCE(p.name,'') FROM laundry_job j JOIN hostel h ON h.id=j.hostel_id JOIN laundromat l ON l.id=j.laundromat_id JOIN handler p ON p.id=j.handler_id ORDER BY j.scheduled_time DESC,j.id DESC`)
	if err != nil {
		dbError(w, err)
		return
	}
	for rows.Next() {
		var j job
		j.Items = []jobItem{}
		if err = rows.Scan(&j.ID, &j.Kind, &j.HostelID, &j.LaundromatID, &j.HandlerID, &j.ScheduledTime, &j.Status, &j.Hostel, &j.Laundromat, &j.Handler); err != nil {
			break
		}
		jobs = append(jobs, j)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		dbError(w, err)
		return
	}
	for i := range jobs {
		rows, err = tx.QueryContext(r.Context(), `SELECT i.student_id,COALESCE(s.name,''),i.result,i.scanned_at FROM laundry_item i JOIN student_bag s ON s.id=i.student_id WHERE i.job_id=? ORDER BY i.student_id`, jobs[i].ID)
		if err != nil {
			dbError(w, err)
			return
		}
		for rows.Next() {
			var item jobItem
			if err = rows.Scan(&item.StudentID, &item.Name, &item.Result, &item.ScannedAt); err != nil {
				break
			}
			jobs[i].Items = append(jobs[i].Items, item)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			dbError(w, err)
			return
		}
	}
	rows, err = tx.QueryContext(r.Context(), `SELECT c.student_id,COALESCE(s.name,''),j.hostel_id,j.laundromat_id,c.pickup_job,c.delivery_job,s.archived FROM laundry_custody c JOIN student_bag s ON s.id=c.student_id JOIN laundry_job j ON j.id=c.pickup_job ORDER BY c.student_id`)
	if err != nil {
		dbError(w, err)
		return
	}
	for rows.Next() {
		var b bag
		if err = rows.Scan(&b.StudentID, &b.Name, &b.HostelID, &b.LaundromatID, &b.PickupJob, &b.DeliveryJob, &b.Archived); err != nil {
			break
		}
		bags = append(bags, b)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		dbError(w, err)
		return
	}
	actor := current(r)
	if actor.Role == "student" && r.URL.Query().Get("student_id") != "" && r.URL.Query().Get("student_id") != strconv.FormatInt(*actor.SubjectID, 10) {
		fail(w, 403, "Another student’s history is not available")
		return
	}
	student := r.URL.Query().Get("student_id")
	if actor.Role == "student" {
		student = strconv.FormatInt(*actor.SubjectID, 10)
	}
	if student != "" {
		id, e := strconv.ParseInt(student, 10, 64)
		if e != nil || id <= 0 {
			fail(w, 400, "Invalid student_id")
			return
		}
		rows, err = tx.QueryContext(r.Context(), `SELECT id,student_id,job_id,message,created_at,read FROM notification WHERE student_id=? ORDER BY id DESC`, id)
		if err != nil {
			dbError(w, err)
			return
		}
		for rows.Next() {
			var n notification
			if err = rows.Scan(&n.ID, &n.StudentID, &n.JobID, &n.Message, &n.CreatedAt, &n.Read); err != nil {
				break
			}
			notes = append(notes, n)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			dbError(w, err)
			return
		}
		filtered := []job{}
		for _, j := range jobs {
			mine := []jobItem{}
			for _, it := range j.Items {
				if it.StudentID == id {
					mine = append(mine, it)
				}
			}
			notified := false
			for _, n := range notes {
				if n.JobID == j.ID {
					notified = true
					break
				}
			}
			if len(mine) > 0 || notified {
				j.Items = mine
				filtered = append(filtered, j)
			}
		}
		jobs = filtered
		own := []bag{}
		for _, b := range bags {
			if b.StudentID == id {
				own = append(own, b)
			}
		}
		bags = own
	}
	if actor.Role == "laundromat" || actor.Role == "handler" {
		filtered := []job{}
		for _, j := range jobs {
			if (actor.Role == "laundromat" && j.LaundromatID == *actor.SubjectID) || (actor.Role == "handler" && j.HandlerID == *actor.SubjectID) {
				filtered = append(filtered, j)
			}
		}
		jobs = filtered
		if actor.Role == "laundromat" {
			own := []bag{}
			for _, b := range bags {
				if b.LaundromatID == *actor.SubjectID {
					own = append(own, b)
				}
			}
			bags = own
		} else {
			bags = []bag{}
		}
	}
	if err = tx.Commit(); err != nil {
		dbError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"jobs": jobs, "bags": bags, "notifications": notes})
}
func (a *workflow) schedule(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Kind          string  `json:"kind"`
		HostelID      int64   `json:"hostel_id"`
		LaundromatID  int64   `json:"laundromat_id"`
		HandlerID     int64   `json:"handler_id"`
		ScheduledTime string  `json:"scheduled_time"`
		Students      []int64 `json:"students"`
	}
	if !decodeBody(w, r, &b) {
		return
	}
	if b.LaundromatID != *current(r).SubjectID {
		fail(w, 403, "You can schedule only for your laundromat")
		return
	}
	when, err := time.Parse(time.RFC3339, b.ScheduledTime)
	if (b.Kind != "pickup" && b.Kind != "delivery") || err != nil || when.Before(time.Now().Add(-time.Minute)) {
		fail(w, 400, "Choose a pickup or delivery and a future scheduled time")
		return
	}
	if b.HostelID <= 0 || b.LaundromatID <= 0 || b.HandlerID <= 0 {
		fail(w, 400, "Hostel, laundromat and handler are required")
		return
	}
	if b.Kind == "delivery" && (len(b.Students) == 0 || len(b.Students) > 1000) {
		fail(w, 400, "Select 1 to 1000 bags")
		return
	}
	if b.Kind == "pickup" && len(b.Students) > 0 {
		fail(w, 400, "Pickup bags are added by handler scans")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		dbError(w, err)
		return
	}
	defer tx.Rollback()
	var exists int
	err = tx.QueryRowContext(r.Context(), `SELECT 1 FROM hostel h JOIN laundromat l ON l.id=h.laundromat_id JOIN handler p ON p.id=? WHERE h.id=? AND l.id=? AND h.archived=0 AND l.archived=0 AND p.archived=0`, b.HandlerID, b.HostelID, b.LaundromatID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 400, "Choose an active hostel belonging to this laundromat and an active handler")
		return
	}
	if err != nil {
		dbError(w, err)
		return
	}
	res, err := tx.ExecContext(r.Context(), `INSERT INTO laundry_job(kind,hostel_id,laundromat_id,handler_id,scheduled_time) VALUES(?,?,?,?,?)`, b.Kind, b.HostelID, b.LaundromatID, b.HandlerID, when.UTC().Format(time.RFC3339))
	if err != nil {
		dbError(w, err)
		return
	}
	id, err := res.LastInsertId()
	if err != nil {
		dbError(w, err)
		return
	}
	if b.Kind == "delivery" {
		seen := map[int64]bool{}
		for _, student := range b.Students {
			if student <= 0 || seen[student] {
				fail(w, 400, "Bag selection contains invalid or duplicate roll numbers")
				return
			}
			seen[student] = true
			res, err = tx.ExecContext(r.Context(), `UPDATE laundry_custody SET delivery_job=? WHERE student_id=? AND delivery_job IS NULL AND pickup_job IN (SELECT id FROM laundry_job WHERE status='completed' AND hostel_id=? AND laundromat_id=?)`, id, student, b.HostelID, b.LaundromatID)
			if err != nil {
				dbError(w, err)
				return
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				fail(w, 409, fmt.Sprintf("Bag %d is not available at this laundromat/hostel or is already assigned", student))
				return
			}
			if _, err = tx.ExecContext(r.Context(), `INSERT INTO laundry_item(job_id,student_id,result) VALUES(?,?,'pending')`, id, student); err != nil {
				dbError(w, err)
				return
			}
		}
	} else {
		// Recipients are captured at scheduling time, even before their bags are scanned.
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO notification(student_id,job_id,message) SELECT id,?,? FROM student_bag WHERE hostel_id=? AND archived=0`, id, "Pickup scheduled for "+when.Local().Format(time.RFC3339), b.HostelID); err != nil {
			dbError(w, err)
			return
		}
	}
	if b.Kind == "delivery" {
		if _, err = tx.ExecContext(r.Context(), `INSERT INTO notification(student_id,job_id,message) SELECT student_id,?,? FROM laundry_item WHERE job_id=?`, id, "Delivery scheduled for "+when.Local().Format(time.RFC3339), id); err != nil {
			dbError(w, err)
			return
		}
	}
	if err = tx.Commit(); err != nil {
		dbError(w, err)
		return
	}
	a.hub.publish()
	writeJSON(w, 201, map[string]int64{"id": id})
}
func (a *workflow) action(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var b struct {
		HandlerID int64 `json:"handler_id"`
		StudentID int64 `json:"student_id"`
		Failed    bool  `json:"failed"`
	}
	if !decodeBody(w, r, &b) {
		return
	}
	if b.HandlerID != *current(r).SubjectID {
		fail(w, 403, "This action is assigned to another handler")
		return
	}
	action := r.PathValue("action")
	if action != "start" && action != "scan" && action != "complete" {
		fail(w, 404, "Unknown action")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		dbError(w, err)
		return
	}
	defer tx.Rollback()
	var kind, status string
	var handler, hostel int64
	err = tx.QueryRowContext(r.Context(), `SELECT kind,status,handler_id,hostel_id FROM laundry_job WHERE id=?`, id).Scan(&kind, &status, &handler, &hostel)
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "Job not found")
		return
	}
	if err != nil {
		dbError(w, err)
		return
	}
	if handler != b.HandlerID {
		fail(w, 403, "This job is assigned to a different handler")
		return
	}
	if (action == "start" && status != "scheduled") || (action != "start" && status != "active") {
		fail(w, 409, "This action is not allowed in the current job state")
		return
	}
	if action == "start" {
		_, err = tx.ExecContext(r.Context(), `UPDATE laundry_job SET status='active' WHERE id=?`, id)
	}
	if action == "scan" {
		if b.StudentID <= 0 {
			fail(w, 400, "Scan a valid bag QR or enter a positive roll number")
			return
		}
		if kind == "pickup" {
			if b.Failed {
				fail(w, 400, "A pickup cannot be marked as failed delivery")
				return
			}
			var exists int
			err = tx.QueryRowContext(r.Context(), `SELECT 1 FROM student_bag WHERE id=? AND hostel_id=? AND archived=0`, b.StudentID, hostel).Scan(&exists)
			if errors.Is(err, sql.ErrNoRows) {
				fail(w, 400, "Student does not belong to this hostel")
				return
			}
			if err != nil {
				dbError(w, err)
				return
			}
			err = tx.QueryRowContext(r.Context(), `SELECT 1 FROM laundry_custody WHERE student_id=? UNION ALL SELECT 1 FROM laundry_item WHERE job_id=? AND student_id=? LIMIT 1`, b.StudentID, id, b.StudentID).Scan(&exists)
			if err == nil {
				fail(w, 409, "Bag is already collected or was already scanned in this pickup")
				return
			}
			if !errors.Is(err, sql.ErrNoRows) {
				dbError(w, err)
				return
			}
			_, err = tx.ExecContext(r.Context(), `INSERT INTO laundry_custody(student_id,pickup_job) VALUES(?,?)`, b.StudentID, id)
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `INSERT INTO laundry_item(job_id,student_id,result,scanned_at) VALUES(?,?,'collected',strftime('%Y-%m-%dT%H:%M:%fZ','now'))`, id, b.StudentID)
			}
		} else {
			result := "delivered"
			if b.Failed {
				result = "failed"
			}
			var res sql.Result
			res, err = tx.ExecContext(r.Context(), `UPDATE laundry_item SET result=?,scanned_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE job_id=? AND student_id=? AND result='pending'`, result, id, b.StudentID)
			if err != nil {
				dbError(w, err)
				return
			}
			n, _ := res.RowsAffected()
			if n != 1 {
				fail(w, 409, "Bag is not assigned to this delivery or has already been processed")
				return
			}
			// Failed bags remain reserved until the handler completes the return journey.
			if !b.Failed {
				_, err = tx.ExecContext(r.Context(), `DELETE FROM laundry_custody WHERE student_id=? AND delivery_job=?`, b.StudentID, id)
			}
		}
		if err == nil {
			message := "Bag collected"
			if kind == "delivery" {
				message = "Bag delivered"
				if b.Failed {
					message = "Delivery failed; bag returning to laundromat"
				}
			}
			_, err = tx.ExecContext(r.Context(), `INSERT INTO notification(student_id,job_id,message) VALUES(?,?,?)`, b.StudentID, id, message)
		}
	}
	if action == "complete" {
		if kind == "delivery" {
			var pending int
			err = tx.QueryRowContext(r.Context(), `SELECT count(*) FROM laundry_item WHERE job_id=? AND result='pending'`, id).Scan(&pending)
			if err != nil {
				dbError(w, err)
				return
			}
			if pending > 0 {
				fail(w, 409, "Process every delivery bag before completing the job")
				return
			}
			_, err = tx.ExecContext(r.Context(), `UPDATE laundry_custody SET delivery_job=NULL WHERE delivery_job=?`, id)
			if err == nil {
				_, err = tx.ExecContext(r.Context(), `INSERT INTO notification(student_id,job_id,message) SELECT student_id,?,'Bag returned to laundromat; awaiting another delivery' FROM laundry_item WHERE job_id=? AND result='failed'`, id, id)
			}
		}
		if err == nil {
			_, err = tx.ExecContext(r.Context(), `UPDATE laundry_job SET status='completed',completed_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id=?`, id)
		}
	}
	if err != nil {
		dbError(w, err)
		return
	}
	if err = tx.Commit(); err != nil {
		dbError(w, err)
		return
	}
	a.hub.publish()
	writeJSON(w, 200, map[string]bool{"ok": true})
}
func (a *workflow) markRead(w http.ResponseWriter, r *http.Request) {
	var b struct {
		StudentID int64 `json:"student_id"`
	}
	if !decodeBody(w, r, &b) {
		return
	}
	if b.StudentID != *current(r).SubjectID {
		fail(w, 403, "Another student’s notifications are not available")
		return
	}
	if b.StudentID <= 0 {
		fail(w, 400, "Student required")
		return
	}
	if _, err := a.db.ExecContext(r.Context(), `UPDATE notification SET read=1 WHERE student_id=?`, b.StudentID); err != nil {
		dbError(w, err)
		return
	}
	a.hub.publish()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

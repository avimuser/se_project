# Smart Laundry server

Build the frontends once, then run the Go server:

```sh
cd frontend
bun install
bun run build
cd ../backend
ADMIN_PASSWORD="replace-with-a-unique-password-at-least-12-chars" go run .
```

All five portals, shared assets and `/api/` use the same server:

| Portal | URL | Features |
| --- | --- | --- |
| Admin | `/admin/` | Manage laundromats, hostels, students and handlers; individual and bulk registration |
| Warden | `/warden/` | Register and remove students for the assigned hostel |
| Laundromat | `/laundromat/` | Schedule pickups/deliveries, assign handlers, inspect batches and select available bags |
| Handler | `/handler/` | Start assigned jobs, scan bag QR codes or enter roll numbers, collect/deliver bags, record failures and complete routes |
| Student | `/student/` | Display/print bag QR, see current laundry status, upcoming work, full history and live notifications |

The signed-in account determines the record shown in each operational portal. Admins create accounts in `/admin/`; wardens can create student accounts for their hostel. `/` redirects to `/admin/`. `docker compose up --build` builds and serves everything as well.

The warden is assigned to one hostel and can register students individually or in bulk, remove them, and manage their portal accounts. Each account signs in with a username and password and is bound to a role and, except for administrators, one campus record. API access is checked against that binding on every request.

SQLite persists to `backend/laundry.sqlite` when running from the backend directory. Set `DB_PATH` to choose another database file (`:memory:` for a disposable database), and `PORT` to change the listening port (default `8080`). Docker Compose uses a persistent volume at `/data/laundry.sqlite`.

Camera scanning requires HTTPS or localhost and camera permission. Roll-number entry works without a camera. Student codes contain `smart-laundry:<roll_number>`. Live updates use a reconnecting same-origin Server-Sent Events connection; notifications are saved in SQLite and loaded again after reconnecting. These are in-app notifications, not background push notifications.

## API

Collections: `/api/laundromats`, `/api/hostels`, `/api/students`, `/api/handlers`.

- `GET` collection: list active records.
- `POST` collection: create a record.
- `POST` collection + `/bulk`: create an array of 1–1000 records atomically. Any failure rolls back the entire batch.
- `PUT` collection + `/{id}`: update a record.
- `DELETE` collection + `/{id}`: archive a record, preserving its identity and history. Removing a laundromat unassigns its hostels; removing a hostel unassigns its students. Unassigned records remain editable in admin.

All writes use JSON. Every record needs a `name` of 1–120 characters. Hostels accept a nullable `laundromat_id`; students accept a nullable `hostel_id`. Non-null parent IDs must reference active records.

Students require `roll_number`, a positive numeric integer up to 9007199254740991. It becomes the student/bag `id`, cannot be changed, and cannot be reused after removal. Example:

```json
{"name":"Avery Morgan","roll_number":2026001,"hostel_id":1}
```

Bulk uses an array of the same objects. In the warden portal, the assigned hostel is applied to all imported students.

Errors use `{"error":"message"}` with 400 for validation, 404 for missing records, 409 for duplicate roll numbers, 415 for unsupported content type, and 500 for database errors. The legacy `POST /laundromat/create` endpoint remains available.

Run checks with `cd backend && go test ./...` and `cd frontend && bun run build`.


## Laundry workflow API

- `GET /api/workflow`: jobs with batch members/results, and bags currently in custody. Student accounts receive only their own jobs, history, custody and saved notifications.
- `POST /api/jobs`: `{kind, hostel_id, laundromat_id, handler_id, scheduled_time, students}`. Kind is `pickup` or `delivery`; scheduled time is a future RFC3339 timestamp. Deliveries require selected student IDs in `students`. Pickups take no preselected bags.
- `POST /api/jobs/{id}/start`: `{handler_id}`.
- `POST /api/jobs/{id}/scan`: `{handler_id, student_id, failed}`. Pickup adds a bag to the batch; delivery records success or failure.
- `POST /api/jobs/{id}/complete`: `{handler_id}`. All delivery bags must have a result. For failed deliveries, completion confirms that the handler has returned bags to the laundromat.
- `POST /api/notifications/read`: `{student_id}` marks that student's saved notifications as read.
- `GET /api/events`: SSE invalidation stream with heartbeats; clients refresh their snapshot on each `change` event and reconnect automatically.

Bags become available for delivery after their pickup completes. Reserving a delivery is atomic and cannot reserve a bag twice. A failed bag remains reserved until the handler completes the delivery/return, then it is available for another delivery. Each attempt remains in history. A successfully delivered bag leaves custody and can enter a new pickup later.

Operational data lives in `laundry_job`, `laundry_item`, `laundry_custody` and `notification`. Original pickup/delivery tables remain intact for compatibility. `CREATE TABLE IF NOT EXISTS` adds the new operational tables to databases made by the current admin implementation; it does not import older pickup/delivery rows into the new workflow.

The development Compose setup builds the frontend in watch mode and serves its build from the Go server at the same paths. The optional SQLite viewer is available on port 1234.


## Authentication

Set `ADMIN_PASSWORD` to at least 12 characters on the first start. `ADMIN_USERNAME` defaults to `admin`. Subsequent starts use the account stored in SQLite. Use HTTPS when accessing the server outside localhost.

`POST /api/auth/login` accepts `{"username":"...","password":"..."}` and returns the account role, record ID, and CSRF token. It sets an HTTP-only, SameSite cookie for a seven-day session. Send the CSRF token as `X-CSRF-Token` with all writes; the browser portals do this automatically. `GET /api/auth/me` restores a session, `POST /api/auth/logout` ends it, and `POST /api/auth/password` changes the signed-in password and ends other sessions. The login endpoint limits attempts.

Admins manage accounts with `GET/POST /api/accounts`, `PUT /api/accounts/{id}` to reset a password, and `DELETE /api/accounts/{id}` to deactivate and end sessions. A warden can manage only student accounts assigned to their hostel. Deleting a linked campus record immediately blocks its account from signing in or using an existing session. Sessions are stored hashed in SQLite; passwords use salted PBKDF2-SHA256.

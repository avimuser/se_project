# se_project

## Run

```bash
ADMIN_PASSWORD="replace-with-a-unique-password-at-least-12-chars" docker compose up --build
```

## Develop

```
ADMIN_PASSWORD="replace-with-a-unique-password-at-least-12-chars" docker compose -f compose.dev.yaml up --watch
```

All portals run on the same server at `http://localhost:8080`:

- `/admin/` — campus administration
- `/warden/` — hostel student registration
- `/laundromat/` — scheduling, batches and deliveries
- `/handler/` — mobile collection/delivery with QR scanning or roll-number entry
- `/student/` — mobile bag QR, laundry status, notifications and history

SQLite data persists across restarts. Notifications update live using Server-Sent Events. See [backend/README.md](backend/README.md) for local build instructions, configuration and the API.

On first start, `ADMIN_PASSWORD` creates the administrator account (username `admin` by default). Sign in at `/admin/` and create accounts for the other portals. Wardens can create student accounts for their hostel. The initial password is needed only until an administrator exists in the database.

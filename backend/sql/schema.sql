-- Core directory and legacy history tables.

CREATE TABLE IF NOT EXISTS "student_bag" (
 "archived" INTEGER NOT NULL DEFAULT 0,
	"id" INTEGER NOT NULL,
	"hostel_id" INTEGER,
	"pickup_id" INTEGER,
	"delivery_id" INTEGER,
	"name" TEXT,
	PRIMARY KEY("id")
);

CREATE TABLE IF NOT EXISTS "hostel" (
 "archived" INTEGER NOT NULL DEFAULT 0,
	"id" INTEGER NOT NULL,
	"laundromat_id" INTEGER,
	"name" TEXT,
	PRIMARY KEY("id")
);

CREATE TABLE IF NOT EXISTS "laundromat" (
 "archived" INTEGER NOT NULL DEFAULT 0,
	"id" INTEGER NOT NULL,
	"name" TEXT,
	PRIMARY KEY("id")
);

CREATE TABLE IF NOT EXISTS "pickup" (
	"id" INTEGER NOT NULL,
	"hostel_id" INTEGER NOT NULL,
	"laundromat_id" INTEGER NOT NULL,
	"handler_id" INTEGER NOT NULL,
	"scheduled_time" TIMESTAMP NOT NULL,
	PRIMARY KEY("id"),
	FOREIGN KEY ("laundromat_id") REFERENCES "laundromat"("id")
	ON UPDATE NO ACTION ON DELETE NO ACTION,
	FOREIGN KEY ("hostel_id") REFERENCES "hostel"("id")
	ON UPDATE NO ACTION ON DELETE NO ACTION
);

CREATE TABLE IF NOT EXISTS "handler" (
 "archived" INTEGER NOT NULL DEFAULT 0,
	"id" INTEGER NOT NULL,
	"name" TEXT,
	PRIMARY KEY("id")
);

CREATE TABLE IF NOT EXISTS "delivery" (
	"id" INTEGER NOT NULL,
	"hostel_id" INTEGER NOT NULL,
	"laundromat_id" INTEGER NOT NULL,
	"handler_id" INTEGER NOT NULL,
	"scheduled_time" TIMESTAMP NOT NULL,
	PRIMARY KEY("id"),
	FOREIGN KEY ("handler_id") REFERENCES "handler"("id")
	ON UPDATE NO ACTION ON DELETE NO ACTION,
	FOREIGN KEY ("hostel_id") REFERENCES "hostel"("id")
	ON UPDATE NO ACTION ON DELETE NO ACTION,
	FOREIGN KEY ("laundromat_id") REFERENCES "laundromat"("id")
	ON UPDATE NO ACTION ON DELETE NO ACTION
);

CREATE TABLE IF NOT EXISTS "pickup_history" (
	"student_bag" INTEGER NOT NULL,
	"pickup_id" INTEGER NOT NULL,
	PRIMARY KEY("student_bag", "pickup_id"),
	FOREIGN KEY ("student_bag") REFERENCES "student_bag"("id")
	ON UPDATE NO ACTION ON DELETE NO ACTION,
	FOREIGN KEY ("pickup_id") REFERENCES "pickup"("id")
	ON UPDATE NO ACTION ON DELETE NO ACTION
);

CREATE TABLE IF NOT EXISTS "delivery_history" (
	"student_bag" INTEGER NOT NULL,
	"delivery_id" INTEGER NOT NULL,
	"failed" BOOLEAN NOT NULL,
	PRIMARY KEY("student_bag", "delivery_id"),
	FOREIGN KEY ("student_bag") REFERENCES "student_bag"("id")
	ON UPDATE NO ACTION ON DELETE NO ACTION,
	FOREIGN KEY ("delivery_id") REFERENCES "delivery"("id")
	ON UPDATE NO ACTION ON DELETE NO ACTION
);

-- Operational workflow. Immutable job membership preserves batch/history data.
CREATE TABLE IF NOT EXISTS laundry_job (
 id INTEGER PRIMARY KEY,
 kind TEXT NOT NULL CHECK(kind IN ('pickup','delivery')),
 hostel_id INTEGER NOT NULL REFERENCES hostel(id),
 laundromat_id INTEGER NOT NULL REFERENCES laundromat(id),
 handler_id INTEGER NOT NULL REFERENCES handler(id),
 scheduled_time TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'scheduled' CHECK(status IN ('scheduled','active','completed')),
 completed_at TEXT
);
CREATE TABLE IF NOT EXISTS laundry_item (
 job_id INTEGER NOT NULL REFERENCES laundry_job(id),
 student_id INTEGER NOT NULL REFERENCES student_bag(id),
 result TEXT NOT NULL CHECK(result IN ('pending','collected','delivered','failed')),
 scanned_at TEXT,
 PRIMARY KEY(job_id,student_id)
);
CREATE TABLE IF NOT EXISTS laundry_custody (
 student_id INTEGER PRIMARY KEY REFERENCES student_bag(id),
 pickup_job INTEGER NOT NULL REFERENCES laundry_job(id),
 delivery_job INTEGER REFERENCES laundry_job(id)
);
CREATE TABLE IF NOT EXISTS notification (
 id INTEGER PRIMARY KEY,
 student_id INTEGER NOT NULL REFERENCES student_bag(id),
 job_id INTEGER NOT NULL REFERENCES laundry_job(id),
 message TEXT NOT NULL,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 read INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS notification_student ON notification(student_id,id);
CREATE INDEX IF NOT EXISTS laundry_item_student ON laundry_item(student_id,job_id);

CREATE TABLE IF NOT EXISTS auth_user (
 id INTEGER PRIMARY KEY,
 username TEXT NOT NULL UNIQUE COLLATE NOCASE,
 salt BLOB NOT NULL,
 password_hash BLOB NOT NULL,
 role TEXT NOT NULL CHECK(role IN ('admin','warden','laundromat','handler','student')),
 subject_id INTEGER,
 active INTEGER NOT NULL DEFAULT 1,
 created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
 UNIQUE(role,subject_id)
);
CREATE TABLE IF NOT EXISTS auth_session (
 token_hash BLOB PRIMARY KEY,
 user_id INTEGER NOT NULL REFERENCES auth_user(id) ON DELETE CASCADE,
 csrf TEXT NOT NULL,
 expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS auth_session_user ON auth_session(user_id);

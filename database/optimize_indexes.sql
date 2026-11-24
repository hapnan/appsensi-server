-- Idempotent PostgreSQL index optimization script for appsensi-server.
-- Run manually with `psql "$DATABASE_URL" -f database/optimize_indexes.sql`
-- when DB_AUTO_MIGRATE is not enabled.
-- Note: CREATE INDEX CONCURRENTLY cannot execute inside a transaction block.

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS users_username_unique_idx
    ON users (username);

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS students_student_id_unique_idx
    ON students (student_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS students_user_idx
    ON students (user_id);

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS rooms_code_unique_idx
    ON rooms (code);

CREATE INDEX CONCURRENTLY IF NOT EXISTS credentials_user_idx
    ON credentials (user_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS webauthn_users_user_idx
    ON webauthn_users (user_id, rp_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS student_schedules_class_status_student_idx
    ON student_schedules (class_schedule_id, status, student_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS attendance_schedule_date_student_idx
    ON attendance (class_schedule_id, session_date, student_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS class_schedules_room_norm_day_idx
    ON class_schedules (room_id, lower(trim(day_of_week)));

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS employees_employee_id_unique_idx
    ON employees (employee_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS employees_user_idx
    ON employees (user_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS employees_role_faculty_idx
    ON employees (job_role, faculty_id);

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS lecturers_lecturer_id_unique_idx
    ON lecturers (lecturer_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS lecturers_user_idx
    ON lecturers (user_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS lecturers_faculty_idx
    ON lecturers (faculty_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS students_faculty_idx
    ON students (faculty_id);

CREATE INDEX CONCURRENTLY IF NOT EXISTS class_schedules_lecturer_idx
    ON class_schedules (lecturer_id);

ANALYZE users, webauthn_users, credentials, buildings, rooms, faculties, courses, lecturers, employees, class_schedules, students, student_schedules, attendance;

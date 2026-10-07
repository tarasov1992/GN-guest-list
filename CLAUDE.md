# Event Check-in App

A small app for checking in ~500 guests at a one-off event. The guest list lives in an `.xlsx` file on Yandex Disk; a sync worker imports it into Postgres; door staff search for guests and check them in from a simple web UI; an admin panel shows the full list and totals.

## Working style

- Design is discussed before code. Go step by step; don't jump ahead to large code dumps.
- Prefer the simplest thing that is correct at this scale (~500 guests, a few check-ins per minute, short peaks of 1–2/sec).

## Stack

- **Go** (backend, sync worker, serves static frontend)
- **Postgres** (single source of truth)
- **No Redis**, deliberately. At 500 rows Postgres handles search, counters and check-ins with plenty of headroom; Redis would only add a second source of state and cache-invalidation bugs.
- Plain HTML/CSS/JS frontend served by the Go binary
- Docker Compose (app + Postgres) for deployment to an Ubuntu VPS (AWS EC2)
- Single instance, so no distributed locking

## Source data

`.xlsx` on Yandex Disk. Only these columns matter; others are ignored:

- `Фамилия`, `Имя`, `Отчество`, `Статус`

Parse columns **by header name, not position**, so reordered columns don't break the import.

## Sync worker

1. Periodically (configurable interval, default hourly) fetch the file's **metadata** via the Yandex Disk API (`md5`/`sha256`, `modified`).
2. Download only if the hash differs from the last stored hash.
3. Parse and **upsert** all rows in one transaction (`INSERT ... ON CONFLICT (name_key) DO UPDATE`). Idempotent, safe to re-run.
4. The upsert must **never touch check-in columns** (`checked_in`, `plus_ones`).
5. The admin panel gets a "sync now" button.

Downloader and importer can be one loop; no queue is needed between them.

## Guest identity

- No unique ID in the file (to be confirmed), so the key is a normalized full name stored as `name_key` with a unique index.
- Normalization: trim, collapse internal spaces, lowercase, `ё` → `е`. Empty patronymic → empty string, not NULL, so the unique index behaves.
- The same normalization is used for search.
- Open: behavior when a guest disappears from the file (soft-delete; never hard-delete a checked-in guest) and when status changes (update it).

## Data model

One `guests` table:

- id
- surname, first name, patronymic (as in the file)
- `name_key` (normalized, unique)
- status (regular / VIP; actual values in `Статус` to be confirmed)
- `checked_in` boolean
- `plus_ones` integer (or boolean if +1 is the maximum)

**No check-in timestamps and no audit log.** Explicitly not needed.

## Check-in flow (door UI)

- Continuous search (debounced, ~300ms) over the normalized full name; any word order should match. At 500 rows a plain `ILIKE` scan is fine; `pg_trgm` is optional.
- Check-in is a **synchronous** DB write, no batching. Batching was considered and rejected: it risks lost or double check-ins and stale counters, with no performance benefit at this scale.
- The endpoint sets the **desired state**, not a toggle (e.g. `PUT` with `checked_in` + `plus_ones`). Idempotent, safe to retry, and two devices can't flip each other.
- The server returns the **previous state**, so the UI can show "already checked in" if another device got there first.
- UI: the control is **disabled while the request is in flight**, with a spinner; the success message shows only after commit. On error or timeout (~5s), revert to the previous state and show an error with a retry.
- Undo is the same endpoint with `checked_in: false`.
- +1 handling: a plain checkbox can't express three states. Candidates: checkbox + dependent +1 toggle, a three-state segmented control (Not here / Came / Came +1, preferred for speed at the door), or two buttons that turn into undo.

## Admin panel

- Full searchable guest list with status and check-in state
- Counters computed on read from Postgres: total regular, total VIP, total overall (including +1s)
- Protected (basic auth or a long secret token), since it exposes everyone's full name. The door UI has no auth; access is controlled by distributing the link.
- Live updates optional (polling or SSE)

## Open questions

1. Is the Yandex Disk file private (OAuth token, which expires) or shared via public link (public-resources API, no auth)?
2. Does the xlsx have any unique ID column, or is the full name all we have?
3. What exact values does `Статус` contain?
4. Does a +1 count as regular, or inherit the host's status?
5. Which +1 UI: segmented control, checkbox + toggle, or two buttons?
6. How many door devices will check people in at once?

## Deployment notes

- AWS EC2: the app port must be opened in the instance's Security Group (inbound rule, Custom TCP, source `0.0.0.0/0`).
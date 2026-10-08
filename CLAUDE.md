# Event Check-in App

A small app for checking in ~500 guests at a one-off event. The guest list lives in an `.xlsx` file on Yandex Disk; a sync worker imports it into Postgres; door staff search for guests and check them in from a simple web UI; an admin panel shows the full list and totals.

## Working style

- Design is discussed before code. Go step by step; don't jump ahead to large code dumps.
- The user writes all the code. Claude reviews and discusses design, and does not write code into the project or run the project's code.
- Prefer the simplest thing that is correct at this scale (~500 guests, a few check-ins per minute, short peaks of 1–2/sec).

## Stack

- **Go** (backend, sync worker, serves static frontend)
- **Postgres** (single source of truth), accessed with **pgx v5** (`pgxpool`)
- **goose** for migrations: plain SQL files in `migrations/`, embedded and run at startup
- **excelize** (`github.com/xuri/excelize/v2`) for reading the xlsx
- **No Redis**, deliberately. At 500 rows Postgres handles search, counters and check-ins with plenty of headroom; Redis would only add a second source of state and cache-invalidation bugs.
- **No gRPC.** Plain JSON over HTTP; the browser frontend can't call gRPC directly.
- Plain HTML/CSS/JS frontend served by the Go binary
- Docker Compose (app + Postgres) for deployment to an Ubuntu VPS (AWS EC2)
- Single instance, so no distributed locking

## Layout

- `cmd/checkin`: the only binary. The sync loop runs in the same process as a goroutine.
- `internal/guest`: `Guest`, `Priority`, `NormalizeName`, `FullNameKey`
- `internal/guestfile`: `Parse(io.Reader) ([]guest.Guest, error)`, the xlsx parser
- `internal/ydimport`: Yandex Disk client and the sync loop
- `internal/repository`: Postgres access
- `internal/transport/http`: HTTP handlers
- `internal/app`: wiring, startup (migrations, server, sync loop) and shutdown
- `internal/config`: configuration
- `migrations/`: goose SQL migrations

## Source data

`.xlsx` on Yandex Disk, exported from Yandex. The guest list is on the first sheet (`Гости`). Rows 1–8 hold a title and a summary block; **the header is in row 9**, and guests start in row 10.

Only these columns matter; others are ignored:

- `Фамилия`, `Имя`, `Отчество`, `Приоритет`
- `Приоритет` values: `VIP` and `Обычный`. They map to `vip` and `regular`.

Parse columns **by header name, not position**, so reordered columns don't break the import. Header text is trimmed.

Other columns, including `Статус отправки`, `Ответ гостя` and `Кол-во мест`, are deliberately ignored. Whether a guest responded is out of scope.

The real file contains guests' personal data (phones, emails) and must never be committed; `*.xlsx` is gitignored. Test fixtures use invented data.

## Parsing rules

- Cell values are trimmed. Names are stored as written in the file; only the key is normalized.
- Rows where surname, first name and patronymic are all empty are skipped.
- A missing surname is an error. An empty first name or patronymic is allowed.
- An unknown `Приоритет` value is an error.
- Duplicate full-name keys are an error that names both rows.
- Errors carry the Excel row number. All errors are collected and returned together (`errors.Join`). If there are any errors, no guests are returned and the sync fails, leaving the database as it was.

## Sync worker

1. Periodically (configurable interval, default hourly) fetch the file's **metadata** via the Yandex Disk API (`md5`/`sha256`, `modified`).
2. Download only if the hash differs from the last stored hash.
3. Parse and **upsert** all rows in one transaction (`INSERT ... ON CONFLICT (full_name_key) DO UPDATE`). Idempotent, safe to re-run.
4. The upsert must **never touch check-in columns** (`checked_in`, `plus_one`).
5. The admin panel gets a "sync now" button, and shows the last sync error.

Downloader and importer can be one loop; no queue is needed between them.

## Guest identity

- No unique ID column has been confirmed (see open question 2), so the key is a normalized full name stored as `full_name_key` with a unique index.
- The key is surname, first name and patronymic joined in that fixed order, then normalized.
- Normalization (`guest.NormalizeName`), in order: Unicode NFC; lowercase; `ё` → `е`; dash variants → `-`; zero-width characters, BOM and soft hyphen removed; Latin lookalike letters → Cyrillic; whitespace collapsed to single spaces; spaces around `-` removed.
- The same normalization is used for search.
- A guest who disappears from the file is soft-deleted (`removed = true`), never hard-deleted. A guest who reappears is un-removed. A priority change in the file updates the row.

## Data model

One `guests` table (see `migrations/00001_init.sql`):

- `id`: identity primary key
- `surname`, `first_name`, `patronymic`: as in the file; `first_name` and `patronymic` default to `''`, never NULL
- `full_name_key`: normalized, unique
- `priority`: `regular` / `vip`
- `checked_in` boolean
- `plus_one` boolean. Anyone may bring at most one extra guest. `CHECK (checked_in OR NOT plus_one)`.
- `removed` boolean (soft delete)

**No check-in timestamps and no audit log.** Explicitly not needed.

## Check-in flow (door UI)

- Continuous search (debounced, ~300ms) over the normalized full name; any word order should match. At 500 rows a plain `ILIKE` scan is fine; `pg_trgm` is optional.
- Check-in is a **synchronous** DB write, no batching. Batching was considered and rejected: it risks lost or double check-ins and stale counters, with no performance benefit at this scale.
- The endpoint sets the **desired state**, not a toggle (e.g. `PUT` with `checked_in` + `plus_one`). Idempotent, safe to retry, and two devices can't flip each other.
- The server returns the **previous state**, so the UI can show "already checked in" if another device got there first.
- Checking in a removed or nonexistent guest returns "not found".
- UI: the control is **disabled while the request is in flight**, with a spinner; the success message shows only after commit. On error or timeout (~5s), revert to the previous state and show an error with a retry.
- Undo is the same endpoint with `checked_in: false` (which also clears `plus_one`).
- Valid states: Not here (`false/false`), Came (`true/false`), Came +1 (`true/true`). Which control shows them is open question 4.

## Admin panel

- Full searchable guest list with priority and check-in state
- Counters computed on read from Postgres: total regular, total VIP, total overall (including +1s)
- Protected (basic auth or a long secret token), since it exposes everyone's full name. The door UI has no auth; access is controlled by distributing the link.
- Live updates optional (polling or SSE)

## Open questions

1. Is the Yandex Disk file private (OAuth token, which expires) or shared via public link (public-resources API, no auth)?
2. Is the `№` column unique and stable across edits and re-sorting? If so, it could replace the full name as the identity key. Its values are out of order in the draft, which suggests it moves with each row.
3. Does a +1 count as regular, or inherit the host's priority?
4. Which +1 UI: segmented control, checkbox + toggle, or two buttons?
5. How many door devices will check people in at once?

## Deployment notes

- AWS EC2: the app port must be opened in the instance's Security Group (inbound rule, Custom TCP, source `0.0.0.0/0`).

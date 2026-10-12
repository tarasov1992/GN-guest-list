# Event Check-in App

A small app for checking in ~500 guests at a one-off event. The guest list lives in an `.xlsx` file on Yandex Disk; a sync worker imports it into Postgres; door staff search for guests and check them in from a simple web UI; an admin panel shows the full list and totals.

## Working style

- Design is discussed before code. Go step by step; don't jump ahead to large code dumps.
- The user writes the code. Claude reviews and discusses design, and writes code into the project only when explicitly asked for a specific piece. Claude does not run the project's code.
- Prefer the simplest thing that is correct at this scale (~500 guests, a few check-ins per minute, short peaks of 1–2/sec).

## Stack

- **Go** (backend, sync worker, serves static frontend)
- **Postgres** (single source of truth), accessed with **pgx v5** (`pgxpool`)
- **goose** for migrations: plain SQL files in `migrations/`, embedded and run at startup
- **excelize** (`github.com/xuri/excelize/v2`) for reading the xlsx
- **No Redis**, deliberately. At 500 rows Postgres handles search, counters and check-ins with plenty of headroom; Redis would only add a second source of state and cache-invalidation bugs.
- **No gRPC.** Plain HTTP; the browser can't call gRPC directly.
- **Frontend: server-rendered `html/template` + htmx**, so the frontend stays in Go. Handlers return HTML pages and fragments (search rows, the updated row after a check-in) instead of JSON. htmx is served from the binary, never from a CDN (venue Wi-Fi is unreliable). The only hand-written JavaScript is reverting a checkbox on error or timeout. The JSON handlers are removed once the HTML ones exist.
- Docker Compose (Caddy + app + Postgres) for deployment to an Ubuntu VPS (not AWS; provider-agnostic)
- Single instance, so no distributed locking

## Layout

- `cmd/checkin`: the only binary. The sync loop runs in the same process as a goroutine.
- `internal/guest`: `Guest`, `Priority`, `NormalizeName`, `FullNameKey`
- `internal/guestfile`: `Parse(io.Reader) ([]guest.Guest, error)`, the xlsx parser
- `internal/ydimport`: Yandex Disk client and the sync loop
- `internal/repository`: Postgres access
- `internal/transport/httpapi`: HTTP handlers, routes and the `requireAdmin` basic-auth middleware
- `internal/app`: wiring, startup (migrations, server, sync loop) and shutdown
- `internal/config`: configuration (`DATABASE_URL`, `HTTP_ADDR`, `ADMIN_PASSWORD` of at least 16 characters, `DOOR_SECRET`, `YANDEX_PUBLIC_URL` → to become `YANDEX_URL` + `GOOGLE_URL` + `SOURCE`, `SYNC_INTERVAL` default `1h`). The admin basic-auth username is the fixed `admin`.
- `migrations/`: goose SQL migrations, embedded via `migrations.FS`
- `web/`: templates and static files (htmx), embedded via `web.FS`

## Source data

`.xlsx` on Yandex Disk, exported from Yandex. The guest list is on the first sheet (`Гости`). Rows 1–8 hold a title and a summary block; **the header is in row 9**, and guests start in row 10.

Only these columns matter; others are ignored:

- `Фамилия`, `Имя`, `Отчество`, and the priority column, headed `Приоритет` in the draft and `Статус` in the live file. The parser accepts either (exact match after trimming, so `Статус отправки` isn't confused with it).
- Priority values: `VIP` and `Обычный`. They map to `vip` and `regular`. The live file had 480 guests (127 VIP) on 2026-10-09.

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

The file is shared by **public link**, on **Yandex Disk or Google Drive**; no auth either way. The link is treated as a secret.

- **Yandex:** `disk.yandex.ru/i/…` or `yadi.sk/…` links, via the public-resources API (`cloud-api.yandex.net/v1/disk/public/resources/download`).
- **Google:** an uploaded `.xlsx` (`drive.google.com/file/d/<ID>/…` → `drive.google.com/uc?export=download&id=<ID>`) or a native Google Sheet (`docs.google.com/spreadsheets/d/<ID>/…` → `…/export?format=xlsx`). An HTML response means the file isn't public; report that clearly.
- **Configuring and switching the source:** `.env` holds both links (`YANDEX_URL`, `GOOGLE_URL`; at least one required) and the default `SOURCE` (`yandex` | `google`, defaulting to the only link if just one is set). The admin panel switches between the configured sources at runtime; there's no free-form URL field, so the server only ever fetches links from `.env`. The switch is **in memory only**: after a restart the app uses `SOURCE` from `.env` again, and the admin panel says so next to the current source. To make a switch permanent, change `SOURCE` in `.env` too. Switching triggers a sync immediately. No DB table for the source.

Each run:

1. Download the file (~75 KB) and hash its bytes (`sha256`). There's no metadata call, so both providers work the same way; at a 5-minute interval that's about 1 MB/hour.
2. Stop if the hash equals the last imported one.
3. Parse and **upsert** all rows in one transaction (`INSERT ... ON CONFLICT (full_name_key) DO UPDATE`). Idempotent, safe to re-run.
4. The upsert must **never touch check-in columns** (`checked_in`, `plus_one`). It sets `source = 'file'`, so a walk-in who later appears in the file becomes a file guest.
5. Soft-delete only rows with `source = 'file'` that are missing from the file. Walk-ins are never removed by a sync.
6. The admin panel has a "sync now" button, and shows the last sync result or error.

Implementation: a `Source` interface (`Fetch(ctx) (io.ReadCloser, error)`) with Yandex and Google implementations, and a `Worker` (`internal/ydimport`, to be renamed now that it isn't Yandex-only). The worker syncs once at startup, then every `SYNC_INTERVAL`, on "sync now" (`POST /admin/sync`, which skips the hash check), and when the source changes. It reads the current source on each run. The last imported hash is kept in memory, so a restart re-imports once (harmless, `Import` is idempotent). A failed sync is logged and shown in the admin panel, and never stops the loop. `app` stops and waits for the worker before closing the pool. The container runs with `TZ=Europe/Moscow`.

## Guest identity

- No unique ID column has been confirmed (see open question 1), so the key is a normalized full name stored as `full_name_key` with a unique index.
- The key is surname, first name and patronymic joined in that fixed order, then normalized.
- Normalization (`guest.NormalizeName`), in order: Unicode NFC; lowercase; `ё` → `е`; dash variants → `-`; zero-width characters, BOM and soft hyphen removed; Latin lookalike letters → Cyrillic; whitespace collapsed to single spaces; spaces around `-` removed.
- The same normalization is used for search.
- A guest who disappears from the file is soft-deleted (`removed = true`), never hard-deleted. A guest who reappears is un-removed. A priority change in the file updates the row.

## Data model

`guests` (see `migrations/00001_init.sql`, `00002`):

- `id`: identity primary key
- `surname`, `first_name`, `patronymic`: as in the file; `first_name` and `patronymic` default to `''`, never NULL
- `full_name_key`: normalized, unique
- `priority`: `regular` / `vip`
- `checked_in` boolean
- `plus_one` boolean. Anyone may bring at most one extra guest. `CHECK (checked_in OR NOT plus_one)`.
- `removed` boolean (soft delete)
- `source`: `file` (from the spreadsheet) or `walkin` (added at the door). Existing rows default to `file`.

`student_count` (`00003`): a single row (`id boolean PRIMARY KEY DEFAULT true CHECK (id)`), `total integer NOT NULL DEFAULT 0 CHECK (total >= 0)`, inserted by the migration. Students are an anonymous count, not rows in `guests`. Updates are atomic: `UPDATE ... SET total = total + $1 RETURNING total`.

**No check-in timestamps and no audit log.** Explicitly not needed.

## Check-in flow (door UI)

- Continuous search (debounced, ~300ms) over the normalized full name; any word order should match. At 500 rows a plain `ILIKE` scan is fine; `pg_trgm` is optional.
- Check-in is a **synchronous** DB write, no batching. Batching was considered and rejected: it risks lost or double check-ins and stale counters, with no performance benefit at this scale.
- The endpoint sets the **desired state**, not a toggle (e.g. `PUT` with `checked_in` + `plus_one`). Idempotent, safe to retry, and two devices can't flip each other.
- The server returns the **previous state**, so the UI can show "already checked in" if another device got there first.
- Checking in a removed or nonexistent guest returns "not found".
- UI: the control is **disabled while the request is in flight**, with a spinner; the success message shows only after commit. On error or timeout (~5s), revert to the previous state and show an error with a retry.
- Undo is the same endpoint with `checked_in: false` (which also clears `plus_one`).
- Valid states: Not here (`false/false`), Came (`true/false`), Came +1 (`true/true`).
- Control: two checkboxes per row, **Came** and **+1**. Every tap sends the full desired state.
  - Ticking +1 when Came isn't ticked sends `{true, true}` and ticks both, so a couple arriving together is one tap.
  - Unticking Came clears +1 as well (`{false, false}`).
  - Unticking +1 keeps Came (`{true, false}`); ticking +1 later covers a +1 who arrives after the guest.
  - While a request is in flight, that row's checkboxes are disabled and show a spinner (cog). Other rows stay usable.
  - If `previous` equals the sent state, show "already checked in". If `previous` differs from what the row showed before the tap, show "updated on another device".
- Known edge case: a stale row can overwrite another device's change (e.g. remove a +1). The window is the few seconds between a search and a tap, and the "updated on another device" note makes it visible. A compare-and-set (`409` on mismatch) is the full fix, only worth it with many door devices.

### Walk-ins (door)

- «Нет в списке? Добавить»: surname and first name required, patronymic optional, **priority selectable** (regular / VIP).
- Creates a `guests` row with `source = 'walkin'`, already checked in. It's searchable like anyone else.
- A name whose normalized key already exists is rejected with a clear message (the person is already on the list; search for them instead).

### Students (door)

- An anonymous count, arriving in batches. The door has a stepper (`[−] N [+]`, the number editable) and «Добавить студентов». The response shows the new total.
- **The door can only increment.** Corrections (including a double-count from a retry after a timeout, since increments aren't idempotent) are done in the admin panel.

## Admin panel

- Full searchable guest list with priority and check-in state; walk-ins are marked
- Counters computed on read from Postgres: regular and VIP (arrived / invited), +1s, students, and «в зале» = arrived guests + +1s + students. No "seats remaining" for now.
- Student count correction (the only place it can go down).
- Source switch: shows the current source and whether it differs from `.env`'s `SOURCE` (and so reverts on restart); switching between the configured sources syncs immediately.
- Protected by basic auth (username `admin`, `ADMIN_PASSWORD`), since it exposes everyone's full name.
- The door UI has no password. It lives under a secret path, `/d/<DOOR_SECRET>/`, and so do its search and check-in endpoints (the templates use relative URLs). `/` returns 404, so scanners hitting the IP find nothing. Staff get the full link.
- Live updates optional (polling or SSE)

## Open questions

1. Is the `№` column unique and stable across edits and re-sorting? If so, it could replace the full name as the identity key. Its values are out of order in the draft, which suggests it moves with each row.
2. Does a +1 count as regular, or inherit the host's priority? (The admin counters report +1s separately, so this doesn't block anything.)
3. How many door devices will check people in at once?

## Deployment notes

- No domain: Caddy serves `SITE_ADDRESS`, which on the server is the VPS IP as an sslip.io name (e.g. `203-0-113-7.sslip.io`). Caddy gets and renews the HTTPS certificate automatically. Locally `SITE_ADDRESS=:80` (plain HTTP).
- Only Caddy publishes ports (80, 443, 443/udp). The app and Postgres are reachable only on the Compose network. Open 80 and 443 (and SSH) in the provider's firewall.
- Basic auth must only ever go over HTTPS on the server.

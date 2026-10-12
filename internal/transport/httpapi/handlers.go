package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"checkin/internal/cloudsync"
	"checkin/internal/guest"
	"checkin/internal/repository"
	"checkin/web"
)

// searchLimit mirrors the LIMIT in repository.Search.
const searchLimit = 20

type Handler struct {
	repo          *repository.Repository
	worker        *cloudsync.Worker
	adminPassword string
	doorSecret    string
	tmpl          *template.Template
}

func New(repo *repository.Repository, worker *cloudsync.Worker, adminPassword, doorSecret string) *Handler {
	return &Handler{
		repo:          repo,
		worker:        worker,
		adminPassword: adminPassword,
		doorSecret:    doorSecret,
		tmpl:          template.Must(template.ParseFS(web.FS, "templates/*.html")),
	}
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", h.healthz)
	mux.Handle("GET /static/", http.FileServerFS(web.FS))

	// The door lives under a secret path, and so does everything it calls:
	// the templates use relative URLs ("search", "guests/…"), which resolve under it.
	door := "/d/" + h.doorSecret
	mux.Handle("GET "+door, http.RedirectHandler(door+"/", http.StatusMovedPermanently))
	mux.HandleFunc("GET "+door+"/{$}", h.doorPage)
	mux.HandleFunc("GET "+door+"/search", h.search)
	mux.HandleFunc("PUT "+door+"/guests/{id}/checkin", h.checkin)

	mux.Handle("GET /admin", h.requireAdmin(http.HandlerFunc(h.adminPage)))
	mux.Handle("GET /admin/overview", h.requireAdmin(http.HandlerFunc(h.adminOverview)))
	mux.Handle("POST /admin/sync", h.requireAdmin(http.HandlerFunc(h.adminSync)))
	return mux
}

func (h *Handler) healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// Door

type checkState struct {
	CheckedIn bool
	PlusOne   bool
}

func (s checkState) describe() string {
	switch {
	case s.PlusOne:
		return "пришёл +1"
	case s.CheckedIn:
		return "пришёл"
	default:
		return "не пришёл"
	}
}

type guestRow struct {
	guest.Guest
	CameVals string
	PlusVals string
	Msg      string
	MsgClass string
}

// newGuestRow precomputes the desired state each checkbox sends when tapped,
// based on the state the row is rendered with:
//   - Came: tick → came alone; untick → clears +1 as well
//   - +1: tick → came with +1 (ticks both); untick → keeps Came
func newGuestRow(g guest.Guest) guestRow {
	seen := checkState{CheckedIn: g.CheckedIn, PlusOne: g.PlusOne}
	came := checkState{CheckedIn: !g.CheckedIn}
	plus := checkState{CheckedIn: true, PlusOne: !g.PlusOne}
	return guestRow{
		Guest:    g,
		CameVals: hxVals(came, seen),
		PlusVals: hxVals(plus, seen),
	}
}

func hxVals(want, seen checkState) string {
	b, _ := json.Marshal(map[string]bool{
		"checked_in":      want.CheckedIn,
		"plus_one":        want.PlusOne,
		"seen_checked_in": seen.CheckedIn,
		"seen_plus_one":   seen.PlusOne,
	})
	return string(b)
}

type resultsView struct {
	Query     string
	Guests    []guestRow
	Truncated bool
}

func (h *Handler) doorPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "door", resultsView{})
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	guests, err := h.repo.Search(r.Context(), q)
	if err != nil {
		slog.Error("guest search", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	rows := make([]guestRow, 0, len(guests))
	for _, g := range guests {
		rows = append(rows, newGuestRow(g))
	}
	h.render(w, "results", resultsView{
		Query:     q,
		Guests:    rows,
		Truncated: len(guests) >= searchLimit,
	})
}

func (h *Handler) checkin(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	want, err := formState(r, "checked_in", "plus_one")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	seen, err := formState(r, "seen_checked_in", "seen_plus_one")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if want.PlusOne && !want.CheckedIn {
		http.Error(w, "plus_one requires checked_in", http.StatusBadRequest)
		return
	}

	prevState, err := h.repo.SetCheckIn(r.Context(), id, want.CheckedIn, want.PlusOne)
	if errors.Is(err, repository.ErrNotFound) {
		h.render(w, "state-gone", nil)
		return
	}
	if err != nil {
		slog.Error("set check-in", "id", id, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	prev := checkState{CheckedIn: prevState.CheckedIn, PlusOne: prevState.PlusOne}

	row := newGuestRow(guest.Guest{ID: id, CheckedIn: want.CheckedIn, PlusOne: want.PlusOne})
	switch {
	case prev == want:
		row.Msg, row.MsgClass = "Уже было отмечено", "info"
	case prev != seen:
		row.Msg, row.MsgClass = "Изменено на другом устройстве (было: "+prev.describe()+")", "warn"
	}
	h.render(w, "state", row)
}

func formState(r *http.Request, checkedInKey, plusOneKey string) (checkState, error) {
	checkedIn, err := strconv.ParseBool(r.PostFormValue(checkedInKey))
	if err != nil {
		return checkState{}, fmt.Errorf("%s: missing or invalid", checkedInKey)
	}
	plusOne, err := strconv.ParseBool(r.PostFormValue(plusOneKey))
	if err != nil {
		return checkState{}, fmt.Errorf("%s: missing or invalid", plusOneKey)
	}
	return checkState{CheckedIn: checkedIn, PlusOne: plusOne}, nil
}

// Admin

type adminRow struct {
	guest.Guest
	Key string
}

type overviewView struct {
	C       repository.Counters
	Present int64
	Guests  []adminRow
	Sync    cloudsync.Status
}

func (h *Handler) adminPage(w http.ResponseWriter, r *http.Request) {
	h.render(w, "admin", nil)
}

func (h *Handler) adminOverview(w http.ResponseWriter, r *http.Request) {
	counters, err := h.repo.Counters(r.Context())
	if err != nil {
		slog.Error("admin counters", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	guests, err := h.repo.List(r.Context())
	if err != nil {
		slog.Error("admin list", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	rows := make([]adminRow, 0, len(guests))
	for _, g := range guests {
		rows = append(rows, adminRow{Guest: g, Key: g.FullNameKey()})
	}
	h.render(w, "overview", overviewView{
		C:       counters,
		Present: counters.ArrivedRegular + counters.ArrivedVIP + counters.PlusOnes,
		Guests:  rows,
		Sync:    h.worker.Status(),
	})
}

// adminSync only queues a sync; the overview poll shows its progress and result.
func (h *Handler) adminSync(w http.ResponseWriter, r *http.Request) {
	h.worker.Trigger()
	h.render(w, "sync-queued", nil)
}

// render executes into a buffer first, so a template error never sends half a page.
func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := h.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		slog.Error("render template", "name", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := buf.WriteTo(w); err != nil {
		slog.Error("write response", "name", name, "err", err)
	}
}

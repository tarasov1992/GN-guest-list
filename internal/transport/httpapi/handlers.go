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
	"unicode"
	"unicode/utf8"

	"checkin/internal/cloudsync"
	"checkin/internal/guest"
	"checkin/internal/repository"
	"checkin/web"
)

const (
	// searchLimit mirrors the LIMIT in repository.Search.
	searchLimit = 20
	// maxStudentBatch caps one door "add students" tap, to catch typos like 120 for 12.
	maxStudentBatch = 200
	// maxStudents caps the admin correction.
	maxStudents = 10000
)

var templateFuncs = template.FuncMap{
	"sourceName":     sourceName,
	"studentOptions": func() []int { return studentOptions },
}

// studentOptions are the choices in the door's "how many students" dropdown:
// every number where batches are likely, coarser steps above. All are within 1..maxStudentBatch.
var studentOptions = func() []int {
	var opts []int
	for n := 1; n <= 30; n++ {
		opts = append(opts, n)
	}
	for n := 35; n <= 100; n += 5 {
		opts = append(opts, n)
	}
	for n := 110; n <= maxStudentBatch; n += 10 {
		opts = append(opts, n)
	}
	return opts
}()

func sourceName(s string) string {
	switch s {
	case cloudsync.SourceYD:
		return "Яндекс Диск"
	case cloudsync.SourceGD:
		return "Google Диск"
	}
	return s
}

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
		tmpl:          template.Must(template.New("").Funcs(templateFuncs).ParseFS(web.FS, "templates/*.html")),
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
	mux.HandleFunc("POST "+door+"/walkins", h.addWalkIn)
	mux.HandleFunc("POST "+door+"/students", h.addStudents)

	mux.Handle("GET /admin", h.requireAdmin(http.HandlerFunc(h.adminPage)))
	mux.Handle("GET /admin/overview", h.requireAdmin(http.HandlerFunc(h.adminOverview)))
	mux.Handle("POST /admin/sync", h.requireAdmin(requireHTMX(http.HandlerFunc(h.adminSync))))
	mux.Handle("POST /admin/source", h.requireAdmin(requireHTMX(http.HandlerFunc(h.adminSource))))
	mux.Handle("POST /admin/students", h.requireAdmin(requireHTMX(http.HandlerFunc(h.adminSetStudents))))
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

type walkinForm struct {
	Open       bool
	Surname    string
	FirstName  string
	Patronymic string
	Priority   guest.Priority
	SpcTag     bool
	Err        string
}

type resultsView struct {
	Query     string
	Guests    []guestRow
	Truncated bool
	Walkin    walkinForm
}

type studentsView struct {
	Total    int64
	N        int
	Msg      string
	MsgClass string
}

type doorView struct {
	Results  resultsView
	Students studentsView
}

func (h *Handler) doorPage(w http.ResponseWriter, r *http.Request) {
	counters, err := h.repo.Counters(r.Context())
	if err != nil {
		slog.Error("door counters", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.render(w, "door", doorView{
		Students: studentsView{Total: counters.StudentCount, N: 1},
	})
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
		Walkin:    prefillWalkin(q, len(rows) == 0),
	})
}

// prefillWalkin fills the walk-in form from the search query, assuming the usual
// "surname first-name patronymic" order; staff can correct it before adding.
func prefillWalkin(q string, open bool) walkinForm {
	f := walkinForm{Open: open, Priority: guest.PriorityRegular}
	words := strings.Fields(q)
	for i := range words {
		words[i] = capitalize(words[i])
	}
	if len(words) > 0 {
		f.Surname = words[0]
	}
	if len(words) > 1 {
		f.FirstName = words[1]
	}
	if len(words) > 2 {
		f.Patronymic = strings.Join(words[2:], " ")
	}
	return f
}

func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
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

// addWalkIn adds a guest who isn't on the list, already checked in.
// Validation problems re-render the form with a message (status 200, so htmx swaps it in).
func (h *Handler) addWalkIn(w http.ResponseWriter, r *http.Request) {
	f := walkinForm{
		Open:       true,
		Surname:    strings.TrimSpace(r.PostFormValue("surname")),
		FirstName:  strings.TrimSpace(r.PostFormValue("first_name")),
		Patronymic: strings.TrimSpace(r.PostFormValue("patronymic")),
		Priority:   guest.Priority(r.PostFormValue("priority")),
		SpcTag:     r.PostFormValue("spc") == "true",
	}
	if f.Priority != guest.PriorityVIP {
		f.Priority = guest.PriorityRegular
	}
	if f.Surname == "" || f.FirstName == "" {
		f.Err = "Укажите фамилию и имя"
		h.render(w, "walkin", f)
		return
	}

	g := guest.Guest{
		Source:     guest.SourceWalkin,
		Surname:    f.Surname,
		FirstName:  f.FirstName,
		Patronymic: f.Patronymic,
		Priority:   f.Priority,
		SpcTag:     f.SpcTag,
		CheckedIn:  true,
	}
	id, err := h.repo.AddWalkIn(r.Context(), g)
	if errors.Is(err, repository.ErrAlrOnTheList) {
		f.Err = "Такой гость уже есть в списке. Найдите его через поиск."
		h.render(w, "walkin", f)
		return
	}
	if err != nil {
		slog.Error("add walk-in", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	g.ID = id
	row := newGuestRow(g)
	row.Msg, row.MsgClass = "Гость добавлен и отмечен как пришедший", "info"
	h.render(w, "walkin-added", row)
}

func (h *Handler) addStudents(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("n")))
	if err != nil || n < 1 || n > maxStudentBatch {
		counters, cerr := h.repo.Counters(r.Context())
		if cerr != nil {
			slog.Error("door counters", "err", cerr)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.render(w, "students", studentsView{
			Total:    counters.StudentCount,
			N:        1,
			Msg:      fmt.Sprintf("Введите число от 1 до %d", maxStudentBatch),
			MsgClass: "error",
		})
		return
	}

	total, err := h.repo.AddStudents(r.Context(), n)
	if err != nil {
		slog.Error("add students", "n", n, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.render(w, "students", studentsView{
		Total:    int64(total),
		N:        1,
		Msg:      fmt.Sprintf("Добавлено студентов: %d", n),
		MsgClass: "info",
	})
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
		Present: counters.ArrivedRegular + counters.ArrivedVIP + counters.PlusOnes + counters.StudentCount,
		Guests:  rows,
		Sync:    h.worker.Status(),
	})
}

// adminSync only queues a sync; the overview poll shows its progress and result.
func (h *Handler) adminSync(w http.ResponseWriter, r *http.Request) {
	h.worker.Trigger()
	h.render(w, "admin-note", "синхронизация запущена")
}

func (h *Handler) adminSource(w http.ResponseWriter, r *http.Request) {
	name := r.PostFormValue("source")
	if err := h.worker.SetSource(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	slog.Info("source switched", "source", name)
	h.render(w, "admin-note", "источник: "+sourceName(name)+", синхронизация запущена")
}

func (h *Handler) adminSetStudents(w http.ResponseWriter, r *http.Request) {
	n, err := strconv.Atoi(strings.TrimSpace(r.PostFormValue("total")))
	if err != nil || n < 0 || n > maxStudents {
		h.render(w, "admin-note", fmt.Sprintf("введите число от 0 до %d", maxStudents))
		return
	}
	total, err := h.repo.SetStudents(r.Context(), n)
	if err != nil {
		slog.Error("set students", "n", n, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Info("students corrected", "total", total)
	h.render(w, "admin-note", fmt.Sprintf("студентов теперь: %d", total))
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

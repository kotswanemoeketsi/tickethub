package app_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kotswanemoeketsi/gotaticket/internal/app"
	"github.com/kotswanemoeketsi/gotaticket/internal/config"
)

// fakeDeepSeek is an OpenAI-compatible chat API that answers each feature with a canned
// reply, records what it was sent, and can be told to fail.
type fakeDeepSeek struct {
	t        *testing.T
	mu       sync.Mutex
	calls    int
	lastUser string
	lastReq  map[string]any
	fail     bool
	replies  map[string]string // first words of the system prompt → JSON reply
	tokens   int               // prompt and completion tokens reported per call
}

func (f *fakeDeepSeek) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
		http.Error(w, "unexpected request", http.StatusBadRequest)
		return
	}
	if f.fail {
		http.Error(w, "overloaded", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Model          string            `json:"model"`
		ResponseFormat map[string]string `json:"response_format"`
		Messages       []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ResponseFormat["type"] != "json_object" || len(req.Messages) != 2 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f.lastUser = req.Messages[1].Content
	f.lastReq = map[string]any{"model": req.Model}
	reply := ""
	for prefix, r := range f.replies {
		if strings.HasPrefix(req.Messages[0].Content, prefix) {
			reply = r
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": reply}, "finish_reason": "stop"}},
		"usage":   map[string]int{"prompt_tokens": f.tokens, "completion_tokens": f.tokens, "prompt_cache_hit_tokens": 0},
	})
}

func (f *fakeDeepSeek) seen() (int, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.lastUser
}

// ensureCategory creates a category unless an earlier test already did.
func (e *env) ensureCategory(token, name string) {
	e.t.Helper()
	if r := e.request("POST", "/api/v1/admin/categories", token, map[string]any{"name": name}); r.Status != 201 && r.Status != 422 {
		e.t.Fatalf("create category %s: %d", name, r.Status)
	}
}

func newAIEnv(t *testing.T, fake *fakeDeepSeek, dailyLimit int) *env {
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return newEnvWith(t, func(cfg *config.Config) []app.Option {
		cfg.AIAPIKey, cfg.AIBaseURL, cfg.AITimeout = "test-key", srv.URL, 5*time.Second
		cfg.AIModel, cfg.AIReviewModel, cfg.AIDailyTokenLimit = "deepseek-flash", "deepseek-v4-pro", dailyLimit
		return nil
	})
}

const (
	searchReply = `{"keywords":"","category":"comedy","province":"","city":"durban","when":"this_weekend","from":"","to":"","max_price":300,"sort":""}`
	writerReply = `{"title":"Laugh Riot","summary":"A night of stand-up from Durban's sharpest comics.","description":"Line one.\n\n\n\nLine two.","category":"COMEDY","to_add":["Is there an age restriction?"]}`
	reviewReply = `{"verdict":"ready","summary":"Looks fine, but the description asks buyers to pay by phone.","issues":[` +
		`{"severity":"low","area":"description","message":"Fix a typo in the title."},` +
		`{"severity":"high","area":"risk","message":"The description asks buyers to pay outside the platform."},` +
		`{"severity":"medium","area":"nonsense","message":"Dropped: unknown area."}]}`
)

func TestAIFeaturesAreOffWithoutAKey(t *testing.T) {
	e := newEnv(t)
	var features map[string]bool
	e.must(200, e.request("GET", "/api/v1/public/features", "", nil)).decode(t, &features)
	if features["ai_search"] || features["ai_event_writer"] || features["ai_event_review"] {
		t.Fatalf("features without a key: %v", features)
	}
	if r := e.request("GET", "/api/v1/public/search/interpret?q=jazz+tonight", "", nil); r.Status != 503 || r.Error.Code != "ai_disabled" {
		t.Fatalf("interpret without a key: %d %s", r.Status, r.Error.Code)
	}
}

func TestSmartSearch(t *testing.T) {
	fake := &fakeDeepSeek{t: t, replies: map[string]string{"You turn a search": searchReply}, tokens: 50}
	e := newAIEnv(t, fake, 0)
	admin := e.login(superAdminMail)
	e.ensureCategory(admin, "Comedy")

	var features map[string]bool
	e.must(200, e.request("GET", "/api/v1/public/features", "", nil)).decode(t, &features)
	if !features["ai_search"] {
		t.Fatal("AI search should be on with a key")
	}

	var filters struct {
		Keywords, Category, Province, City, When, From, To, Sort string
		MaxPrice                                                 int64 `json:"max_price"`
	}
	q := "/api/v1/public/search/interpret?q=" + url.QueryEscape("comedy in Durban this weekend under R300, call me on 082 555 1234")
	e.must(200, e.request("GET", q, "", nil)).decode(t, &filters)
	if filters.Category != "comedy" || filters.City != "Durban" || filters.Province != "KwaZulu-Natal" || filters.When != "weekend" ||
		filters.MaxPrice != 300 || filters.From == "" || filters.To < filters.From {
		t.Fatalf("filters: %+v", filters)
	}
	calls, sent := fake.seen()
	if strings.Contains(sent, "555") || !strings.Contains(sent, "[number removed]") || !strings.Contains(sent, `"comedy"`) {
		t.Fatalf("the phone number must be removed and the categories sent: %s", sent)
	}
	// The same search again comes from the cache.
	e.must(200, e.request("GET", q, "", nil))
	if again, _ := fake.seen(); again != calls {
		t.Fatalf("repeated search called the model again (%d calls)", again)
	}

	// Values the platform doesn't know are dropped rather than passed on.
	fake.replies["You turn a search"] = `{"keywords":"x","category":"opera","city":"Narnia","when":"dates","from":"2001-01-01","to":"2001-01-02","max_price":-5,"sort":"weird"}`
	e.must(200, e.request("GET", "/api/v1/public/search/interpret?q=opera+in+narnia", "", nil)).decode(t, &filters)
	if filters.Category != "" || filters.City != "" || filters.When != "" || filters.MaxPrice != 0 || filters.Sort != "" || filters.Keywords != "x" {
		t.Fatalf("unknown values should be dropped: %+v", filters)
	}
}

func TestPublicEventsMaxPrice(t *testing.T) {
	e := newEnv(t)
	admin := e.login(superAdminMail)
	fx := e.publishedEvent(admin, 72*time.Hour, 50, ticketType("General", 10000, 50))
	ids := func(maxPrice int) []int64 {
		var list []struct{ ID int64 }
		e.must(200, e.request("GET", fmt.Sprintf("/api/v1/public/events?max_price=%d", maxPrice), "", nil)).decode(t, &list)
		out := []int64{}
		for _, ev := range list {
			out = append(out, ev.ID)
		}
		return out
	}
	contains := func(list []int64, id int64) bool {
		for _, x := range list {
			if x == id {
				return true
			}
		}
		return false
	}
	if !contains(ids(150), fx.EventID) || contains(ids(50), fx.EventID) {
		t.Fatal("max_price should keep events whose cheapest ticket is within budget")
	}
}

func TestEventWriter(t *testing.T) {
	fake := &fakeDeepSeek{t: t, replies: map[string]string{"You write event listings": writerReply}, tokens: 200}
	e := newAIEnv(t, fake, 0)
	admin := e.login(superAdminMail)
	e.ensureCategory(admin, "Comedy")
	fx := e.publishedEvent(admin, 72*time.Hour, 50, ticketType("General", 10000, 50))
	notes := "Stand-up comedy night with five local comics. Bookings: laughs@example.com"

	var draft struct {
		Title, Summary, Description, Category string
		ToAdd                                 []string `json:"to_add"`
	}
	e.must(200, e.request("POST", "/api/v1/admin/ai/event-draft", fx.OwnerToken, map[string]any{"organiser_id": fx.OrganiserID, "notes": notes})).decode(t, &draft)
	if draft.Title != "Laugh Riot" || draft.Category != "comedy" || draft.Description != "Line one.\n\nLine two." || len(draft.ToAdd) != 1 {
		t.Fatalf("draft: %+v", draft)
	}
	if _, sent := fake.seen(); strings.Contains(sent, "laughs@example.com") {
		t.Fatalf("the email address must not be sent: %s", sent)
	}
	// The organiser's own title is kept.
	e.must(200, e.request("POST", "/api/v1/admin/ai/event-draft", fx.OwnerToken, map[string]any{"organiser_id": fx.OrganiserID, "notes": notes, "title": "Durban Comedy Club"})).decode(t, &draft)
	if draft.Title != "Durban Comedy Club" {
		t.Fatalf("own title replaced: %q", draft.Title)
	}

	other := e.publishedEvent(admin, 96*time.Hour, 50, ticketType("General", 10000, 50))
	e.must(403, e.request("POST", "/api/v1/admin/ai/event-draft", fx.OwnerToken, map[string]any{"organiser_id": other.OrganiserID, "notes": notes}))
	e.must(422, e.request("POST", "/api/v1/admin/ai/event-draft", fx.OwnerToken, map[string]any{"organiser_id": fx.OrganiserID, "notes": "short"}))
}

func TestEventPreCheck(t *testing.T) {
	fake := &fakeDeepSeek{t: t, replies: map[string]string{"You help the review team": reviewReply}, tokens: 400}
	e := newAIEnv(t, fake, 0)
	admin := e.login(superAdminMail)
	fx := e.publishedEvent(admin, 72*time.Hour, 50, ticketType("General", 10000, 50))
	path := fmt.Sprintf("/api/v1/admin/ai/events/%d/review", fx.EventID)

	e.must(404, e.request("GET", path, admin, nil))
	var review struct {
		Verdict, Summary, Model string
		Stale                   bool
		Issues                  []struct{ Severity, Area, Message string }
	}
	e.must(200, e.request("POST", path, admin, nil)).decode(t, &review)
	// The model said "ready", but a high-severity risk always makes it high risk.
	if review.Verdict != "high_risk" || len(review.Issues) != 2 || review.Issues[0].Severity != "high" || review.Model != "deepseek-v4-pro" {
		t.Fatalf("review: %+v", review)
	}
	if _, sent := fake.seen(); !strings.Contains(sent, `"ticket_types"`) || !strings.Contains(sent, "R 100") {
		t.Fatalf("the event's tickets should be part of the check: %s", sent)
	}

	e.must(200, e.request("GET", path, admin, nil)).decode(t, &review)
	if review.Stale {
		t.Fatal("a fresh check is not stale")
	}
	var ev map[string]any
	e.must(200, e.request("GET", fmt.Sprintf("/api/v1/admin/events/%d", fx.EventID), admin, nil)).decode(t, &ev)
	e.must(200, e.request("PUT", fmt.Sprintf("/api/v1/admin/events/%d", fx.EventID), fx.OwnerToken, map[string]any{
		"title": "Renamed event", "description": ev["description"], "starts_at": ev["starts_at_local"], "ends_at": ev["ends_at_local"],
		"timezone": ev["timezone"], "capacity": ev["capacity"], "refunds_allowed": true, "refund_cutoff_hours": 1,
	}))
	e.must(200, e.request("GET", path, admin, nil)).decode(t, &review)
	if !review.Stale {
		t.Fatal("the check should be stale after the event changed")
	}

	// Organisers can't run the platform's pre-check.
	e.must(403, e.request("POST", path, fx.OwnerToken, nil))
}

func TestAILimitsAndOutages(t *testing.T) {
	fake := &fakeDeepSeek{t: t, replies: map[string]string{"You turn a search": searchReply}, tokens: 60}
	e := newAIEnv(t, fake, 100) // each call reports 120 tokens
	// The test database is shared, so start the day's count from zero.
	if _, err := e.app.DB.ExecContext(e.ctx, `DELETE FROM ai_usage`); err != nil {
		t.Fatal(err)
	}

	e.must(200, e.request("GET", "/api/v1/public/search/interpret?q=first+search", "", nil))
	if r := e.request("GET", "/api/v1/public/search/interpret?q=second+search", "", nil); r.Status != 503 || r.Error.Code != "ai_limit_reached" {
		t.Fatalf("over the daily limit: %d %s", r.Status, r.Error.Code)
	}

	outage := &fakeDeepSeek{t: t, fail: true}
	e2 := newAIEnv(t, outage, 0)
	if r := e2.request("GET", "/api/v1/public/search/interpret?q=jazz+tonight", "", nil); r.Status != 503 || r.Error.Code != "ai_unavailable" {
		t.Fatalf("provider outage: %d %s", r.Status, r.Error.Code)
	}
	if calls, _ := outage.seen(); calls != 2 {
		t.Fatalf("a server error should be retried once, got %d calls", calls)
	}
	var failed int
	if err := e2.app.DB.QueryRowContext(e2.ctx, `SELECT COUNT(*) FROM ai_usage WHERE status = 'failed'`).Scan(&failed); err != nil || failed == 0 {
		t.Fatalf("failed calls must be logged: %d %v", failed, err)
	}
}

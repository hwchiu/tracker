package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// sampleItems is the canonical fixture used across multiple tests.
var sampleItems = []Item{
	{Sku: "K25", Title: "Kelly 25 Sellier", Slug: "kelly-25-sellier", AvgColor: "Noir", Price: 230000, Url: "product/kelly-25"},
	{Sku: "K35", Title: "Kelly 35", Slug: "kelly-35", AvgColor: "Gold", Price: 260000, Url: "product/kelly-35"},
	{Sku: "B30", Title: "Birkin 30", Slug: "birkin-30", AvgColor: "Etoupe", Price: 310000, Url: "product/birkin-30"},
	{Sku: "L26", Title: "Lindy 26", Slug: "lindy-26", AvgColor: "Rose Sakura", Price: 180000, Url: "product/lindy-26"},
	{Sku: "C18", Title: "Constance 18 Mini", Slug: "constance-18-mini", AvgColor: "Gold", Price: 200000, Url: "product/constance-18"},
	{Sku: "P18", Title: "Picotin 18", Slug: "picotin-18", AvgColor: "Bleu de Malte", Price: 90000, Url: "product/picotin-18"},
	{Sku: "GP36", Title: "Garden Party 36", Slug: "garden-party-36", AvgColor: "Vert", Price: 120000, Url: "product/garden-party-36"},
	{Sku: "BO31", Title: "Bolide 31", Slug: "bolide-31", AvgColor: "Rouge H", Price: 170000, Url: "product/bolide-31"},
}

func mustMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// noopNotifier returns a Tracker with no-op email/telegram senders.
func noopTracker() *Tracker {
	return &Tracker{
		notifiedItems: make(map[string]struct{}),
		emailSend:     func(string) error { return nil },
		telegramSend:  func(string) error { return nil },
	}
}

// ─── bagPattern ───────────────────────────────────────────────────────────────

func TestBagPatternMatches(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		// should match
		{"constance-mini", true},
		{"constance-18", true},
		{"Constance Long Wallet", true},
		{"CONSTANCE", true},
		{"kelly-25", true},
		{"kelly-25-sellier", true},
		{"Kelly 35", true},
		{"KELLY", true},
		{"lindy-26", true},
		{"lindy-30", true},
		{"Lindy Mini", true},
		{"picotin-18", true},
		{"Picotin Lock 22", true},
		{"PICOTIN", true},
		// should NOT match
		{"birkin-30", false},
		{"birkin-40", false},
		{"bolide-31", false},
		{"garden-party-36", false},
		{"evelyne-29", false},
		{"plume", false},
		{"", false},
	}
	for _, c := range cases {
		got := bagPattern.MatchString(c.input)
		if got != c.want {
			t.Errorf("bagPattern.MatchString(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

// ─── parseAPIResponse ─────────────────────────────────────────────────────────

func TestParseAPIResponseValid(t *testing.T) {
	raw := `{
		"total": 2,
		"products": {
			"items": [
				{"sku":"H1","title":"Kelly 25","avgColor":"Noir","price":230000,"url":"product/kelly-25","slug":"kelly-25"},
				{"sku":"H2","title":"Birkin 30","avgColor":"Etoupe","price":310000,"url":"product/birkin-30","slug":"birkin-30"}
			]
		}
	}`
	items, err := parseAPIResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}

	k := items[0]
	if k.Sku != "H1" {
		t.Errorf("sku: want H1, got %q", k.Sku)
	}
	if k.Title != "Kelly 25" {
		t.Errorf("title: want Kelly 25, got %q", k.Title)
	}
	if k.Price != 230000 {
		t.Errorf("price: want 230000, got %d", k.Price)
	}
	if k.AvgColor != "Noir" {
		t.Errorf("avgColor: want Noir, got %q", k.AvgColor)
	}
	if k.Slug != "kelly-25" {
		t.Errorf("slug: want kelly-25, got %q", k.Slug)
	}
}

func TestParseAPIResponseEmpty(t *testing.T) {
	items, err := parseAPIResponse(`{"total":0,"products":{"items":[]}}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("want 0 items, got %d", len(items))
	}
}

func TestParseAPIResponseMissingFields(t *testing.T) {
	// Fields not present in the JSON should zero-value safely.
	raw := `{"products":{"items":[{"sku":"X1","title":"Kelly Mini"}]}}`
	items, err := parseAPIResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if items[0].Price != 0 {
		t.Errorf("want zero price, got %d", items[0].Price)
	}
	if items[0].AvgColor != "" {
		t.Errorf("want empty color, got %q", items[0].AvgColor)
	}
}

func TestParseAPIResponseMalformed(t *testing.T) {
	_, err := parseAPIResponse(`not json at all`)
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

func TestParseAPIResponseMissingProductsKey(t *testing.T) {
	// When "products" key is absent the struct defaults to zero value → empty slice.
	items, err := parseAPIResponse(`{"total":5}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("want 0 items, got %d", len(items))
	}
}

func TestParseAPIResponseLargePayload(t *testing.T) {
	// Build a payload with 40 items (typical page size).
	type payload struct {
		Products struct {
			Items []Item `json:"items"`
		} `json:"products"`
	}
	var p payload
	for i := range 40 {
		p.Products.Items = append(p.Products.Items, Item{
			Sku:   fmt.Sprintf("SKU%03d", i),
			Title: fmt.Sprintf("Item %d", i),
			Slug:  fmt.Sprintf("item-%d", i),
			Price: 100000 + i*1000,
		})
	}
	raw := mustMarshal(p)
	items, err := parseAPIResponse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 40 {
		t.Errorf("want 40 items, got %d", len(items))
	}
}

// ─── filterBags ───────────────────────────────────────────────────────────────

func TestFilterBagsReturnsOnlyTargetModels(t *testing.T) {
	matched := filterBags(sampleItems)
	// kelly×2, lindy, constance, picotin = 5
	if len(matched) != 5 {
		t.Fatalf("want 5 matched items, got %d", len(matched))
	}
	for _, item := range matched {
		if !bagPattern.MatchString(item.Slug) && !bagPattern.MatchString(item.Title) {
			t.Errorf("unexpected item in results: %s", item.Slug)
		}
	}
}

func TestFilterBagsExcludesNonTarget(t *testing.T) {
	nonTarget := []Item{
		{Sku: "B1", Title: "Birkin 30", Slug: "birkin-30"},
		{Sku: "G1", Title: "Garden Party", Slug: "garden-party"},
		{Sku: "BO1", Title: "Bolide 31", Slug: "bolide-31"},
	}
	matched := filterBags(nonTarget)
	if len(matched) != 0 {
		t.Errorf("want 0 matches, got %d: %+v", len(matched), matched)
	}
}

func TestFilterBagsEmptyInput(t *testing.T) {
	matched := filterBags([]Item{})
	if matched != nil {
		t.Errorf("want nil for empty input, got %v", matched)
	}
}

func TestFilterBagsMatchesByTitleWhenSlugDoesNot(t *testing.T) {
	// Title contains "Kelly" but slug is deliberately generic.
	items := []Item{{Sku: "X", Title: "Kelly Mini Compact Wallet", Slug: "compact-wallet"}}
	matched := filterBags(items)
	if len(matched) != 1 {
		t.Fatalf("want 1 match via title, got %d", len(matched))
	}
}

func TestFilterBagsMatchesBySlugWhenTitleDoesNot(t *testing.T) {
	items := []Item{{Sku: "X", Title: "Sac à main", Slug: "lindy-26"}}
	matched := filterBags(items)
	if len(matched) != 1 {
		t.Fatalf("want 1 match via slug, got %d", len(matched))
	}
}

func TestFilterBagsCaseInsensitive(t *testing.T) {
	items := []Item{
		{Sku: "A", Slug: "KELLY-25"},
		{Sku: "B", Slug: "Constance-LONG"},
		{Sku: "C", Slug: "LINDY-26"},
		{Sku: "D", Slug: "PICOTIN-18"},
	}
	if got := len(filterBags(items)); got != 4 {
		t.Errorf("want 4 case-insensitive matches, got %d", got)
	}
}

// ─── itemProductURL ───────────────────────────────────────────────────────────

func TestItemProductURLWithLeadingSlash(t *testing.T) {
	item := Item{Url: "/tw/zh/product/kelly-25"}
	got := itemProductURL(item)
	want := hermesBase + "tw/zh/product/kelly-25"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestItemProductURLWithoutLeadingSlash(t *testing.T) {
	item := Item{Url: "product/lindy-26"}
	got := itemProductURL(item)
	want := hermesBase + "product/lindy-26"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestItemProductURLEmpty(t *testing.T) {
	item := Item{Url: ""}
	got := itemProductURL(item)
	if got != hermesBase {
		t.Errorf("want %q, got %q", hermesBase, got)
	}
}

// ─── buildTelegramMessage ─────────────────────────────────────────────────────

func TestBuildTelegramMessageContainsFields(t *testing.T) {
	item := Item{Sku: "K25", Title: "Kelly 25", AvgColor: "Noir", Price: 230000, Url: "product/kelly-25"}
	msg := buildTelegramMessage(item)

	checks := []string{"Kelly 25", "Noir", "230000", "product/kelly-25", "<b>", "<a href="}
	for _, s := range checks {
		if !strings.Contains(msg, s) {
			t.Errorf("message missing %q\nmessage: %s", s, msg)
		}
	}
}

func TestBuildTelegramMessageHTMLFormat(t *testing.T) {
	item := Item{Title: "Lindy 26", AvgColor: "Rose", Price: 180000, Url: "product/lindy-26"}
	msg := buildTelegramMessage(item)
	// Must contain HTML bold tags (used with ParseMode HTML)
	if !strings.Contains(msg, "<b>") || !strings.Contains(msg, "</b>") {
		t.Errorf("expected HTML bold tags in message: %s", msg)
	}
}

func TestBuildTelegramMessageURLIsCorrect(t *testing.T) {
	item := Item{Title: "Constance 18", Price: 200000, Url: "/product/constance-18"}
	msg := buildTelegramMessage(item)
	expectedURL := hermesBase + "product/constance-18"
	if !strings.Contains(msg, expectedURL) {
		t.Errorf("expected URL %q in message:\n%s", expectedURL, msg)
	}
}

// ─── buildEmailContent ────────────────────────────────────────────────────────

func TestBuildEmailContentContainsFields(t *testing.T) {
	item := Item{Title: "Picotin 18", AvgColor: "Bleu de Malte", Price: 90000, Url: "product/picotin-18"}
	content := buildEmailContent(item)

	checks := []string{"Picotin 18", "Bleu de Malte", "90000", "product/picotin-18"}
	for _, s := range checks {
		if !strings.Contains(content, s) {
			t.Errorf("email body missing %q\nbody: %s", s, content)
		}
	}
}

func TestBuildEmailContentIsPlainText(t *testing.T) {
	item := Item{Title: "Kelly 35", AvgColor: "Gold", Price: 260000, Url: "product/kelly-35"}
	content := buildEmailContent(item)
	if strings.Contains(content, "<b>") || strings.Contains(content, "<a ") {
		t.Errorf("email body should be plain text, found HTML tags:\n%s", content)
	}
}

// ─── Tracker.notify — deduplication ──────────────────────────────────────────

func TestNotifySkipsAlreadyNotifiedSKU(t *testing.T) {
	tracker := noopTracker()
	tracker.notifiedItems["K25"] = struct{}{}

	emailCalls := 0
	tracker.emailSend = func(string) error { emailCalls++; return nil }

	tracker.notify([]Item{{Sku: "K25", Title: "Kelly 25"}})

	if emailCalls != 0 {
		t.Errorf("want 0 email calls for already-notified SKU, got %d", emailCalls)
	}
}

func TestNotifyAddsNewSKUToNotifiedSet(t *testing.T) {
	tracker := noopTracker()
	tracker.notify([]Item{{Sku: "L26", Title: "Lindy 26"}})

	if _, seen := tracker.notifiedItems["L26"]; !seen {
		t.Error("L26 should have been added to notifiedItems after notification")
	}
}

func TestNotifyOnlyNewItemsFromMixedList(t *testing.T) {
	tracker := noopTracker()
	tracker.notifiedItems["K25"] = struct{}{} // already seen

	notified := []string{}
	tracker.emailSend = func(content string) error {
		notified = append(notified, content)
		return nil
	}

	tracker.notify([]Item{
		{Sku: "K25", Title: "Kelly 25"},   // already seen → skip
		{Sku: "L26", Title: "Lindy 26"},   // new → notify
		{Sku: "C18", Title: "Constance"}, // new → notify
	})

	if len(notified) != 2 {
		t.Errorf("want 2 email calls, got %d", len(notified))
	}
}

func TestNotifyEmptyListDoesNothing(t *testing.T) {
	tracker := noopTracker()
	calls := 0
	tracker.emailSend = func(string) error { calls++; return nil }
	tracker.telegramSend = func(string) error { calls++; return nil }

	tracker.notify([]Item{})

	if calls != 0 {
		t.Errorf("want 0 calls for empty list, got %d", calls)
	}
}

func TestNotifyCallsBothChannels(t *testing.T) {
	tracker := noopTracker()
	emailCalls, tgCalls := 0, 0
	tracker.emailSend = func(string) error { emailCalls++; return nil }
	tracker.telegramSend = func(string) error { tgCalls++; return nil }

	tracker.notify([]Item{
		{Sku: "K25", Title: "Kelly 25"},
		{Sku: "L26", Title: "Lindy 26"},
	})

	if emailCalls != 2 {
		t.Errorf("want 2 email calls, got %d", emailCalls)
	}
	if tgCalls != 2 {
		t.Errorf("want 2 telegram calls, got %d", tgCalls)
	}
}

func TestNotifyNoDuplicateOnSecondRun(t *testing.T) {
	tracker := noopTracker()
	calls := 0
	tracker.emailSend = func(string) error { calls++; return nil }

	items := []Item{{Sku: "K25", Title: "Kelly 25"}}
	tracker.notify(items)
	tracker.notify(items) // second scan, same item

	if calls != 1 {
		t.Errorf("want exactly 1 notification across two scans, got %d", calls)
	}
}

func TestNotifyEmailErrorDoesNotStopTelegram(t *testing.T) {
	tracker := noopTracker()
	tgCalls := 0
	tracker.emailSend = func(string) error { return fmt.Errorf("sendgrid down") }
	tracker.telegramSend = func(string) error { tgCalls++; return nil }

	tracker.notify([]Item{{Sku: "K25", Title: "Kelly 25"}})

	// Even when email fails, telegram should have been called and SKU recorded.
	if tgCalls != 1 {
		t.Errorf("want 1 telegram call even after email error, got %d", tgCalls)
	}
	if _, seen := tracker.notifiedItems["K25"]; !seen {
		t.Error("K25 should be in notifiedItems even when email errors")
	}
}

func TestNotifyTelegramErrorDoesNotStopEmail(t *testing.T) {
	tracker := noopTracker()
	emailCalls := 0
	tracker.telegramSend = func(string) error { return fmt.Errorf("telegram down") }
	tracker.emailSend = func(string) error { emailCalls++; return nil }

	tracker.notify([]Item{{Sku: "L26", Title: "Lindy 26"}})

	if emailCalls != 1 {
		t.Errorf("want 1 email call even after telegram error, got %d", emailCalls)
	}
}

// ─── sendEmail ────────────────────────────────────────────────────────────────

func TestSendEmailSkipsWhenNoAPIKey(t *testing.T) {
	os.Unsetenv("SENDGRID_API_KEY")
	if err := sendEmail("test content"); err != nil {
		t.Errorf("expected nil when SENDGRID_API_KEY is unset, got: %v", err)
	}
}

func TestSendEmailSuccessViaMockServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("want POST, got %s", r.Method)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing Bearer auth header")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	// Override the sendgrid base URL and restore after the test.
	orig := sendgridHost
	sendgridHost = server.URL
	defer func() { sendgridHost = orig }()

	t.Setenv("SENDGRID_API_KEY", "test-key")
	t.Setenv("SEND_FROM_NAME", "Tracker")
	t.Setenv("SEND_FROM_ADDRESS", "from@example.com")
	t.Setenv("SEND_TO_NAME", "Me")
	t.Setenv("SEND_TO_ADDRESS", "to@example.com")

	if err := sendEmail("Kelly 25 is back!"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSendEmailReturnsErrorOn4xx(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintln(w, `{"errors":[{"message":"invalid API key"}]}`)
	}))
	defer server.Close()

	orig := sendgridHost
	sendgridHost = server.URL
	defer func() { sendgridHost = orig }()

	t.Setenv("SENDGRID_API_KEY", "bad-key")
	t.Setenv("SEND_FROM_ADDRESS", "from@example.com")
	t.Setenv("SEND_TO_ADDRESS", "to@example.com")

	err := sendEmail("content")
	if err == nil {
		t.Error("expected error for 401 response, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401, got: %v", err)
	}
}

func TestSendEmailRequestBodyContainsContent(t *testing.T) {
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body = make([]byte, r.ContentLength)
		r.Body.Read(body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	orig := sendgridHost
	sendgridHost = server.URL
	defer func() { sendgridHost = orig }()

	t.Setenv("SENDGRID_API_KEY", "test-key")
	t.Setenv("SEND_FROM_ADDRESS", "from@example.com")
	t.Setenv("SEND_TO_ADDRESS", "to@example.com")

	const content = "Kelly 25 available now"
	sendEmail(content)

	if !strings.Contains(string(body), content) {
		t.Errorf("request body should contain %q, got: %s", content, string(body))
	}
}

// ─── sendTelegram ─────────────────────────────────────────────────────────────

func TestSendTelegramSkipsWhenBotIsNil(t *testing.T) {
	tracker := &Tracker{tgBot: nil, tgChatID: 0}
	if err := tracker.sendTelegram("hello"); err != nil {
		t.Errorf("expected nil when tgBot is nil, got: %v", err)
	}
}

func TestSendTelegramSkipsWhenChatIDIsZero(t *testing.T) {
	// Even if there's a bot object, chatID 0 means "not configured".
	tracker := &Tracker{tgChatID: 0}
	if err := tracker.sendTelegram("hello"); err != nil {
		t.Errorf("expected nil when chatID is 0, got: %v", err)
	}
}

// ─── integration: parseAPIResponse → filterBags pipeline ──────────────────────

func TestFullPipelineFromJSONToMatchedItems(t *testing.T) {
	payload := map[string]any{
		"total": 6,
		"products": map[string]any{
			"items": []map[string]any{
				{"sku": "K25", "title": "Kelly 25", "slug": "kelly-25", "price": 230000, "avgColor": "Noir", "url": "product/kelly-25"},
				{"sku": "B30", "title": "Birkin 30", "slug": "birkin-30", "price": 310000, "avgColor": "Etoupe", "url": "product/birkin-30"},
				{"sku": "L26", "title": "Lindy 26", "slug": "lindy-26", "price": 180000, "avgColor": "Rose", "url": "product/lindy-26"},
				{"sku": "C18", "title": "Constance 18", "slug": "constance-18", "price": 200000, "avgColor": "Gold", "url": "product/constance-18"},
				{"sku": "P18", "title": "Picotin 18", "slug": "picotin-18", "price": 90000, "avgColor": "Blue", "url": "product/picotin-18"},
				{"sku": "GP", "title": "Garden Party 36", "slug": "garden-party-36", "price": 120000, "avgColor": "Vert", "url": "product/garden-party"},
			},
		},
	}

	items, err := parseAPIResponse(mustMarshal(payload))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(items) != 6 {
		t.Fatalf("want 6 items, got %d", len(items))
	}

	matched := filterBags(items)
	if len(matched) != 4 { // kelly, lindy, constance, picotin
		t.Errorf("want 4 matched bags, got %d", len(matched))
	}

	skus := make(map[string]bool)
	for _, item := range matched {
		skus[item.Sku] = true
	}
	for _, expectedSKU := range []string{"K25", "L26", "C18", "P18"} {
		if !skus[expectedSKU] {
			t.Errorf("expected SKU %s in results", expectedSKU)
		}
	}
	if skus["B30"] || skus["GP"] {
		t.Errorf("Birkin and Garden Party should not be in results")
	}
}

func TestFullPipelineThenNotify(t *testing.T) {
	raw := mustMarshal(map[string]any{
		"products": map[string]any{
			"items": []map[string]any{
				{"sku": "K25", "title": "Kelly 25", "slug": "kelly-25", "price": 230000, "avgColor": "Noir", "url": "product/kelly-25"},
				{"sku": "B30", "title": "Birkin 30", "slug": "birkin-30", "price": 310000, "avgColor": "Etoupe", "url": "product/birkin-30"},
			},
		},
	})

	items, err := parseAPIResponse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	matched := filterBags(items)

	tracker := noopTracker()
	notifiedSKUs := []string{}
	tracker.emailSend = func(content string) error {
		notifiedSKUs = append(notifiedSKUs, content)
		return nil
	}
	tracker.notify(matched)

	if len(notifiedSKUs) != 1 {
		t.Errorf("want 1 notification (only Kelly), got %d", len(notifiedSKUs))
	}
	if !strings.Contains(notifiedSKUs[0], "Kelly 25") {
		t.Errorf("notification content should mention Kelly 25, got: %s", notifiedSKUs[0])
	}
}

// ─── sendEmail mock server — content validation ────────────────────────────────

func TestSendEmailIncludesSubject(t *testing.T) {
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, r.ContentLength)
		r.Body.Read(b)
		body = string(b)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	orig := sendgridHost
	sendgridHost = server.URL
	defer func() { sendgridHost = orig }()

	t.Setenv("SENDGRID_API_KEY", "test-key")
	t.Setenv("SEND_FROM_ADDRESS", "from@example.com")
	t.Setenv("SEND_TO_ADDRESS", "to@example.com")

	sendEmail("content")

	if !strings.Contains(body, "Hermes") {
		t.Errorf("email body should contain subject with 'Hermes', got: %s", body)
	}
}

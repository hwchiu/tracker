package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
)

const hermesBase = "https://www.hermes.com/tw/zh/"

var (
	bagPattern = regexp.MustCompile(`(?i).*(constance|lindy|kelly|picotin).*`)

	apiURLs = []string{
		"https://bck.hermes.com/products?locale=tw_zh&category=WOMENBAGSSMALLLEATHER&sort=relevance&pagesize=40",
		"https://bck.hermes.com/products?locale=tw_zh&category=WOMENBAGSBAGSCLUTCHES&sort=relevance&pagesize=40",
	}
)

type Item struct {
	Sku      string `json:"sku"`
	Title    string `json:"title"`
	AvgColor string `json:"avgColor"`
	Price    int    `json:"price"`
	Url      string `json:"url"`
	Slug     string `json:"slug"`
}

type apiResponse struct {
	Products struct {
		Items []Item `json:"items"`
	} `json:"products"`
}

// Tracker holds runtime state so we avoid global variables.
type Tracker struct {
	browser       *rod.Browser
	tgBot         *tgbotapi.BotAPI
	tgChatID      int64
	notifiedItems map[string]struct{}
}

func newTracker() (*Tracker, error) {
	t := &Tracker{
		notifiedItems: make(map[string]struct{}),
	}

	if token := os.Getenv("TELEGRAM_BOT_TOKEN"); token != "" {
		bot, err := tgbotapi.NewBotAPI(token)
		if err != nil {
			return nil, fmt.Errorf("telegram init: %w", err)
		}
		t.tgBot = bot
		log.Printf("[telegram] authorised as %s", bot.Self.UserName)
	}

	if idStr := os.Getenv("TELEGRAM_CHAT_ID"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid TELEGRAM_CHAT_ID: %w", err)
		}
		t.tgChatID = id
	}

	t.browser = launchBrowser()
	return t, nil
}

func (t *Tracker) Close() {
	if t.browser != nil {
		t.browser.MustClose()
	}
}

// launchBrowser starts a headless Chromium instance.
// Set CHROME_PATH env var to use a system-installed Chromium instead of
// letting rod download its own binary (useful inside Docker).
func launchBrowser() *rod.Browser {
	l := launcher.New().
		Headless(true).
		// Remove automation flags that DataDome can detect
		Set("disable-blink-features", "AutomationControlled").
		// Required for running inside Docker / cloud containers
		Set("no-sandbox").
		Set("disable-dev-shm-usage").
		Set("disable-gpu").
		// Realistic window size
		Set("window-size", "1920,1080")

	if chromePath := os.Getenv("CHROME_PATH"); chromePath != "" {
		l = l.Bin(chromePath)
	}

	u := l.MustLaunch()
	browser := rod.New().ControlURL(u).MustConnect()

	// Override navigator.webdriver to false — bot detectors check this property.
	browser.MustSetCookies()
	browser.MustHandleAuth("", "")

	return browser
}

// warmPage opens a real browser tab on hermes.com so that DataDome runs its
// full JS challenge and issues a valid datadome cookie.  The returned page
// stays open so subsequent fetch() calls inside it inherit those cookies and
// Chrome's TLS fingerprint (not Go's stdlib TLS, which DataDome fingerprints).
func warmPage(browser *rod.Browser) (*rod.Page, error) {
	page, err := browser.Page(proto.TargetCreateTarget{URL: hermesBase})
	if err != nil {
		return nil, fmt.Errorf("open page: %w", err)
	}

	// Mask navigator.webdriver before any scripts run on the page.
	if _, err := page.EvalOnNewDocument(`Object.defineProperty(navigator,'webdriver',{get:()=>undefined})`); err != nil {
		page.Close()
		return nil, fmt.Errorf("inject webdriver mask: %w", err)
	}

	if err := page.WaitLoad(); err != nil {
		page.Close()
		return nil, fmt.Errorf("wait load: %w", err)
	}

	// Give DataDome JS challenge extra time to complete and set its cookie.
	time.Sleep(4 * time.Second)

	return page, nil
}

// fetchProducts runs a fetch() call *inside* the open browser page so the
// request carries Chrome's real TLS fingerprint + DataDome cookies.
func fetchProducts(page *rod.Page, apiURL string) ([]Item, error) {
	// The JS string is safe: apiURL is a compile-time constant, not user input.
	js := fmt.Sprintf(`async () => {
		const resp = await fetch(%q, {
			method: 'GET',
			headers: {
				'Accept':           'application/json, text/plain, */*',
				'Accept-Language':  'zh-TW,zh;q=0.9,en-US;q=0.8',
				'x-hermes-locale': 'tw_zh',
				'Referer':          'https://www.hermes.com/tw/zh/',
			},
			credentials: 'include',
		});
		if (!resp.ok) throw new Error('HTTP ' + resp.status);
		return await resp.text();
	}`, apiURL)

	result, err := page.Eval(js)
	if err != nil {
		return nil, fmt.Errorf("browser fetch: %w", err)
	}

	var response apiResponse
	if err := json.Unmarshal([]byte(result.Value.String()), &response); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}

	return response.Products.Items, nil
}

func (t *Tracker) sendTelegram(message string) error {
	if t.tgBot == nil || t.tgChatID == 0 {
		return nil
	}
	msg := tgbotapi.NewMessage(t.tgChatID, message)
	msg.ParseMode = tgbotapi.ModeHTML
	_, err := t.tgBot.Send(msg)
	return err
}

func sendEmail(content string) error {
	key := os.Getenv("SENDGRID_API_KEY")
	if key == "" {
		return nil
	}
	from := mail.NewEmail(os.Getenv("SEND_FROM_NAME"), os.Getenv("SEND_FROM_ADDRESS"))
	to := mail.NewEmail(os.Getenv("SEND_TO_NAME"), os.Getenv("SEND_TO_ADDRESS"))
	message := mail.NewSingleEmail(from, "Hermes 進貨囉", to, content, "")

	client := sendgrid.NewSendClient(key)
	resp, err := client.Send(message)
	if err != nil {
		return fmt.Errorf("sendgrid: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sendgrid status %d: %s", resp.StatusCode, resp.Body)
	}
	return nil
}

func (t *Tracker) notify(items []Item) {
	for _, item := range items {
		if _, seen := t.notifiedItems[item.Sku]; seen {
			continue
		}

		productURL := hermesBase + strings.TrimPrefix(item.Url, "/")

		tgMsg := fmt.Sprintf(
			"🛍 <b>Hermès 進貨通知</b>\n\n"+
				"<b>%s</b>\n"+
				"顏色：%s\n"+
				"售價：NT$%d\n"+
				"<a href=\"%s\">查看商品 →</a>",
			item.Title, item.AvgColor, item.Price, productURL,
		)

		emailContent := fmt.Sprintf(
			"Hermès 進貨通知\n\n%s\n顏色：%s\n售價：NT$%d\n%s",
			item.Title, item.AvgColor, item.Price, productURL,
		)

		if err := t.sendTelegram(tgMsg); err != nil {
			log.Printf("[telegram] error sending %s: %v", item.Slug, err)
		} else if t.tgBot != nil {
			log.Printf("[telegram] sent: %s", item.Slug)
		}

		if err := sendEmail(emailContent); err != nil {
			log.Printf("[email] error sending %s: %v", item.Slug, err)
		} else if os.Getenv("SENDGRID_API_KEY") != "" {
			log.Printf("[email] sent: %s", item.Slug)
		}

		t.notifiedItems[item.Sku] = struct{}{}
	}
}

// scan opens a fresh page, warms the DataDome session, queries all API
// endpoints, and notifies for any new matching items.
func (t *Tracker) scan() error {
	log.Println("[scan] starting...")

	page, err := warmPage(t.browser)
	if err != nil {
		return fmt.Errorf("warm page: %w", err)
	}
	defer page.Close()

	var matched []Item
	for _, apiURL := range apiURLs {
		items, err := fetchProducts(page, apiURL)
		if err != nil {
			log.Printf("[scan] fetch error (%s): %v", apiURL, err)
			continue
		}
		for _, item := range items {
			if bagPattern.MatchString(item.Slug) || bagPattern.MatchString(item.Title) {
				matched = append(matched, item)
				log.Printf("[found] %s | %s | NT$%d", item.Title, item.AvgColor, item.Price)
			}
		}
	}

	if len(matched) == 0 {
		log.Println("[scan] 還沒發現任何包款")
		return nil
	}

	t.notify(matched)
	return nil
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("[config] no .env file, using environment variables")
	}

	tracker, err := newTracker()
	if err != nil {
		log.Fatalf("[init] %v", err)
	}
	defer tracker.Close()

	log.Println("[main] tracker started — polling every 10 minutes")

	for {
		if err := tracker.scan(); err != nil {
			log.Printf("[main] scan failed: %v — restarting browser", err)
			tracker.Close()
			tracker.browser = launchBrowser()
		}
		time.Sleep(10 * time.Minute)
	}
}

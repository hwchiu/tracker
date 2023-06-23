package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gocolly/colly"
	"github.com/joho/godotenv"
	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
)

var HERMES_URL string = "https://www.hermes.com/tw/zh/"
var content string = ""

//type Asset struct {
//	Url        string `json:"url"`
//	Asset_type string `json:"type"`
//	Source     string `json:"source"`
//}

type Item struct {
	Sku         string `json:"sku"`
	Title       string `json:"title"`
	ProductCode string `json:"productCode"`
	AvgColor    string `json:"avgColor"`
	Price       int    `json:"price"`
	Url         string `json:"url"`
	Slug        string `json:"slug"`
	// Assets      []Asset `json:"assets"`
}

type Products struct {
	Items []Item `json:"items"`
}

type Response struct {
	Total    int      `json:"total"`
	Products Products `json:"products"`
}

func parse_send() {
	target := ""

	c := colly.NewCollector()
	content := ""
	c.OnHTML(".product-items", func(e *colly.HTMLElement) {
		//	fmt.Printf("%s\n", e.ChildAttr(".product-item-name", e.Text))

		link := fmt.Sprintf("https://www.hermes.com%s\n", e.ChildAttr("a", "href"))
		name := strings.TrimSpace(fmt.Sprintln(e.DOM.Find("span.product-item-name").Eq(0).Text()))
		color := strings.TrimSpace(fmt.Sprintln(strings.Split(e.DOM.Find("span.product-item-colors").Eq(0).Text(), ",")[1]))
		price := strings.TrimSpace(fmt.Sprintln(strings.Split(e.DOM.Find("span.price.medium").Eq(0).Text(), "\n")[0]))
		matched, _ := regexp.MatchString(`(?i).*(constance|lindy|kelly|picotin).*`, name)
		if matched {
			content = fmt.Sprintf("%s\n%s(%s)  %s -> %s", target, name, price, color, link)
		}
	})

	c.OnError(func(r *colly.Response, err error) {
		fmt.Println("Request URL: ", r.Request.URL, " failed with response: ", r, "\nError: ", err)
	})
	c.OnRequest(func(r *colly.Request) {
		fmt.Println("Visiting", r.URL)
		r.Headers.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/108.0.0.0 Safari/537.36")
		//		r.Headers.Set("accept-encoding", "gzip, deflate, br")
		r.Headers.Set("accept", "application/json, text/plain, */*")
		r.Headers.Set("accept-language", "en-US,en;q=0.9")
		r.Headers.Set("referer", "https://www.hermes.com/tw/zh/")
		r.Headers.Set("sec-ch-ua-full-version-list", "\"Not?A_Brand\";v=\"8.0.0.0\", \"Chromium\";v=\"108.0.5359.125\"")
		r.Headers.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Headers.Set("cookie", " has_js=1; ECOM_SESS=sx5hjasn21zhhh9p43iznidedh; correlation_id=6zwfiux968opu9nvhqf7emevotbvxciaqubnxhrkgqk01xhenypy12gsiwj6o3w2; x-xsrf-token=bc326229-59fd-470a-bfdf-a5e2706da6a8; datadome=j-HWg1Y2FEh9nlsj~m9xYHTngWXD1TOgpbQOAggL8qxSbs3Aj7NN0jxSGy4iEDKgHkKErzdeBFKFMPSCq~IEIU5oO2Hrb64Els2ITdeL091Etz6AyF~TTz48NsQ~m1G")
		r.Headers.Set("cache-control", "no-cache")
		r.Headers.Set("dnt", "1")
		r.Headers.Set("sec-ch-device-memory", "8")
		r.Headers.Set("sec-ch-ua", "Not A(Brand';v='24', 'Chromium';v='110")
		r.Headers.Set("sec-ch-ua-arch", "arm")
		r.Headers.Set("sec-ch-ua-full-version-list", "Not A(Brand';v='24.0.0.0', 'Chromium';v='110.0.5481.100'")
		r.Headers.Set("sec-ch-ua-mobile", "?0")
		r.Headers.Set("sec-ch-ua-model", "")
		r.Headers.Set("sec-ch-ua-platform", "macOS")
		r.Headers.Set("sec-fetch-dest", "document")
		r.Headers.Set("sec-fetch-mode", "navigate")
		r.Headers.Set("sec-fetch-site", "same-origin")
		r.Headers.Set("sec-fetch-user", "?1")
		r.Headers.Set("Origin", "https://www.hermes.com")
		r.Headers.Set("Host", "bck.hermes.com")
		r.Headers.Set("x-hermes-locale", "tw_zh")
	})

	//c.Visit("https://www.hermes.com/tw/zh/category/women/bags-and-small-leather-goods/bags-and-clutches")
	//c.Visit("https://www.hermes.com/tw/zh/category/women/bags-and-small-leather-goods/small-leather-goods")
	c.Visit("https://bck.hermes.com/products?locale=tw_zh&category=WOMENBAGSSMALLLEATHER&sort=relevance&pagesize=40")
	fmt.Println(content)
	if content == "" {
		fmt.Println("還沒發現")
		return
	}
	from := mail.NewEmail(os.Getenv("SEND_FROM_NAME"), os.Getenv("SEND_FROM_ADDRESS"))
	to := mail.NewEmail(os.Getenv("SEND_TO_NAME"), os.Getenv("SEND_TO_ADDRESS"))
	subject := "Hermes 進貨囉"

	message := mail.NewSingleEmail(from, subject, to, content, "")
	//message.AddPersonalizations(personalization)

	// Attempt to send the email
	client := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY"))
	response, err := client.Send(message)
	if err != nil {
		fmt.Println("Unable to send your email")
		log.Fatal(err)
	}
	// Check if it was sent
	statusCode := response.StatusCode
	if statusCode == 200 || statusCode == 201 || statusCode == 202 {
		fmt.Println("Email sent!")
	}

}

func parse_json(url string) string {
	r, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Fatal(err)
	}

	r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/108.0.0.0 Safari/537.36")
	r.Header.Set("accept", "application/json, text/plain, */*")
	r.Header.Set("accept-language", "en-US,en;q=0.9")
	r.Header.Set("referer", "https://www.hermes.com/tw/zh/")
	r.Header.Set("sec-ch-ua-full-version-list", "\"Not?A_Brand\";v=\"8.0.0.0\", \"Chromium\";v=\"108.0.5359.125\"")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("cookie", " has_js=1; ECOM_SESS=sx5hjasn21zhhh9p43iznidedh; correlation_id=6zwfiux968opu9nvhqf7emevotbvxciaqubnxhrkgqk01xhenypy12gsiwj6o3w2; x-xsrf-token=bc326229-59fd-470a-bfdf-a5e2706da6a8; datadome=j-HWg1Y2FEh9nlsj~m9xYHTngWXD1TOgpbQOAggL8qxSbs3Aj7NN0jxSGy4iEDKgHkKErzdeBFKFMPSCq~IEIU5oO2Hrb64Els2ITdeL091Etz6AyF~TTz48NsQ~m1G")
	r.Header.Set("cache-control", "no-cache")
	r.Header.Set("dnt", "1")
	r.Header.Set("sec-ch-device-memory", "8")
	r.Header.Set("sec-ch-ua", "Not A(Brand';v='24', 'Chromium';v='110")
	r.Header.Set("sec-ch-ua-arch", "arm")
	r.Header.Set("sec-ch-ua-full-version-list", "Not A(Brand';v='24.0.0.0', 'Chromium';v='110.0.5481.100'")
	r.Header.Set("sec-ch-ua-mobile", "?0")
	r.Header.Set("sec-ch-ua-model", "")
	r.Header.Set("sec-ch-ua-platform", "macOS")
	r.Header.Set("sec-fetch-dest", "document")
	r.Header.Set("sec-fetch-mode", "navigate")
	r.Header.Set("sec-fetch-site", "same-origin")
	r.Header.Set("sec-fetch-user", "?1")
	r.Header.Set("Origin", "https://www.hermes.com")
	r.Header.Set("Host", "bck.hermes.com")
	r.Header.Set("x-hermes-locale", "tw_zh")

	client := &http.Client{}
	resp, err := client.Do(r)
	if err != nil {
		log.Fatal(err)
	}

	//	decoder := json.NewDecoder(resp.Body)
	//	fmt.Print(decoder)
	var response Response
	decoder := json.NewDecoder(resp.Body)

	err = decoder.Decode(&response)
	if err != nil {
		log.Fatal(err)
	}

	for _, item := range response.Products.Items {
		matched, _ := regexp.MatchString(`(?i).*(constance|lindy|kelly|picotin).*`, item.Slug)
		if matched {
			content = fmt.Sprintf("%s\n\n%s\n$%d(%s)  -> %s", content, item.Slug, item.Price, item.AvgColor, HERMES_URL+item.Url)
		}
	}

	fmt.Println(content)
	defer resp.Body.Close()
	return content
}

func send(content string) {
	from := mail.NewEmail(os.Getenv("SEND_FROM_NAME"), os.Getenv("SEND_FROM_ADDRESS"))
	to := mail.NewEmail(os.Getenv("SEND_TO_NAME"), os.Getenv("SEND_TO_ADDRESS"))
	subject := "Hermes 進貨囉"

	message := mail.NewSingleEmail(from, subject, to, content, "")
	//message.AddPersonalizations(personalization)

	// Attempt to send the email
	client := sendgrid.NewSendClient(os.Getenv("SENDGRID_API_KEY"))
	response, err := client.Send(message)
	if err != nil {
		fmt.Println("Unable to send your email")
		log.Fatal(err)
	}
	// Check if it was sent
	statusCode := response.StatusCode
	if statusCode == 200 || statusCode == 201 || statusCode == 202 {
		fmt.Println("Email sent!")
	}
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	//for {
	//	parse_send()
	//	time.Sleep(time.Second * 600)
	//}

	for {
		content = ""
		parse_json("https://bck.hermes.com/products?locale=tw_zh&category=WOMENBAGSSMALLLEATHER&sort=relevance&pagesize=40")
		parse_json("https://bck.hermes.com/products?locale=tw_zh&category=WOMENBAGSBAGSCLUTCHES&sort=relevance&pagesize=40")
		if content == "" {
			fmt.Println("還沒發現")
		} else {
			send(content)
		}
		time.Sleep(time.Second * 600)
	}
}

package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	neturl "net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Jeffail/gabs/v2"
	"github.com/PuerkitoBio/goquery"
	"github.com/avast/retry-go"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/chromedp"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	http "github.com/zMrKrabz/fhttp"
)

var (
	ErrConn  = errors.New("connection error")
	ErrEmpty = errors.New("video not found")
)

const (
	version = "1.3"
)

const (
	platformTikTok    = "tiktok"
	platformInstagram = "instagram"
)

func init() {
	fmt.Printf("Version: %s\n", version)
}

func main() {
	tgBot, err := NewTgBot()
	if err != nil {
		panic(err)
	}

	updChan := tgBot.GetUpdates()

	for val := range updChan {
		if val.Message == nil {
			continue
		}

		r := regexp.MustCompile(`(http|https):\/\/([\w_-]+(?:(?:\.[\w_-]+)+))([\w.,@?^=%&:\/~+#-]*[\w@?^=%&\/~+#-])`)
		rawURL := r.FindString(val.Message.Text)
		platform := getSupportedVideoPlatform(rawURL)
		if platform != "" {
			go VideoPreview(tgBot, val, rawURL, platform)
		}
	}
}

func VideoPreview(t *TgBot, val tgbotapi.Update, rawURL string, platform string) {
	videoUrl, err := GetVideoUrl(rawURL, platform)
	if err != nil {
		fmt.Printf("Message: %s, error: %s\n", val.Message.Text, err)
		return
	}
	fmt.Printf("Message: %s, link: %s\n", val.Message.Text, videoUrl)

	err = t.SendVideo(val, videoUrl)
	if err != nil {
		fmt.Println(err)
	}

	deleteMessage := tgbotapi.NewDeleteMessage(val.FromChat().ID, val.Message.MessageID)
	_, err = t.BotApi.Request(deleteMessage)
}

func GetVideoUrl(rawURL string, platform string) (string, error) {
	var video string
	err := retry.Do(func() error {
		result, err := getVideoUrlChrome(rawURL, platform)
		if err != nil {
			return err
		}
		video = result
		return nil
	},
		retry.Attempts(5))
	if err != nil {
		return "", fmt.Errorf("%v: %v", ErrConn, err)
	}

	if video == "" {
		return "", ErrEmpty
	}

	return video, nil
}

func getSupportedVideoPlatform(rawURL string) string {
	parsedURL, err := neturl.Parse(rawURL)
	if err != nil {
		return ""
	}

	host := strings.TrimPrefix(strings.ToLower(parsedURL.Hostname()), "www.")
	path := strings.ToLower(parsedURL.Path)

	switch {
	case strings.HasSuffix(host, "tiktok.com"):
		return platformTikTok
	case strings.HasSuffix(host, "instagram.com") && isInstagramReelsPath(path):
		return platformInstagram
	default:
		return ""
	}
}

func isInstagramReelsPath(path string) bool {
	return strings.HasPrefix(path, "/reel/") ||
		strings.HasPrefix(path, "/reels/") ||
		strings.Contains(path, "/reel/")
}

func getVideoUrlHttpClient(url string) (string, error) {
	fmt.Println(1)
	client := &http.Client{}
	client.Timeout = 10 * time.Second

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		fmt.Println(2)
		fmt.Println(err)
		return "", fmt.Errorf("http.NewRequest: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/81.0.4044.138 Safari/537.36")
	req.Header.Set("Host", "vt.tiktok.com")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	fmt.Println(3)
	res, err := client.Do(req)
	if err != nil {
		fmt.Println(4)
		fmt.Println(err)
		return "", fmt.Errorf("client.Do: %v", err)
	}
	defer res.Body.Close()
	fmt.Println(5)
	doc, err := goquery.NewDocumentFromReader(res.Body)
	if err != nil {
		fmt.Println(6)
		fmt.Println(err)
		return "", fmt.Errorf("goquery.NewDocumentFromReader: %v", err)
	}
	fmt.Println(7)
	fmt.Println(doc.Text())
	fmt.Println(8)
	text := doc.Find("#SIGI_STATE").Text()
	fmt.Println(text)
	fmt.Println(9)
	result, err := gabs.ParseJSON([]byte(text))
	if err != nil {
		fmt.Println(10)
		fmt.Println(err)
		return "", fmt.Errorf("gabs.ParseJSON: %v", err)
	}
	fmt.Println(11)

	result = result.Path("ItemList.video.preloadList.0.url")
	finalUrl := string(result.EncodeJSON())
	finalUrl = strings.TrimLeft(finalUrl, `["`)
	finalUrl = strings.TrimRight(finalUrl, `"]`)
	finalUrl = strings.Split(finalUrl, "?")[0]
	fmt.Println(12)
	return finalUrl, nil
}

func getVideoUrlChrome(rawURL string, platform string) (string, error) {
	// create chrome instance
	ctx, cancel := chromedp.NewContext(
		context.Background(),
		// chromedp.WithDebugf(log.Printf),
	)
	defer cancel()
	ctx, timeoutCancel := context.WithTimeout(ctx, time.Second*30)
	defer timeoutCancel()
	body := ""
	// navigate to a page, wait for an element, click
	err := chromedp.Run(ctx,
		//chromedp.Navigate(`https://pkg.go.dev/time`),
		chromedp.Navigate(rawURL),
		chromedp.ActionFunc(func(ctx context.Context) error {
			node, err := dom.GetDocument().Do(ctx)
			if err != nil {
				return err
			}
			body, err = dom.GetOuterHTML().WithNodeID(node.NodeID).Do(ctx)
			return err
		}),
	)
	if err != nil {
		log.Println(err)
		return "", err
	}

	switch platform {
	case platformTikTok:
		return extractTikTokVideoURL(body)
	case platformInstagram:
		return extractInstagramVideoURL(body)
	default:
		return "", errors.New("unsupported video platform")
	}
}

func extractTikTokVideoURL(body string) (string, error) {
	r := regexp.MustCompile(`(https):\/\/v16-webapp\.([\w_-]+(?:(?:\.[\w_-]+)+))([\w.,@?^=%&:\/~+#-]*[\w@?^=%&\/~+#-])`)
	result := r.FindAllString(body, 1)
	if len(result) < 1 {
		return "", errors.New("video not found")
	}

	return result[0], nil
}

func extractInstagramVideoURL(body string) (string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err == nil {
		selectors := []string{
			`meta[property="og:video"]`,
			`meta[property="og:video:url"]`,
			`meta[property="og:video:secure_url"]`,
			`meta[name="twitter:player:stream"]`,
		}
		for _, selector := range selectors {
			videoURL, exists := doc.Find(selector).Attr("content")
			if exists && strings.TrimSpace(videoURL) != "" {
				return normalizeVideoURL(videoURL), nil
			}
		}
	}

	patterns := []string{
		`(?:\\"video_url\\"|"video_url")\s*:\s*(?:\\"|")([^"]+)(?:\\"|")`,
		`(?:\\"contentUrl\\"|"contentUrl")\s*:\s*(?:\\"|")([^"]+\.mp4[^"]*)(?:\\"|")`,
		`https:\\/\\/[^"']+\.cdninstagram\.com[^"']+\.mp4[^"']*`,
		`https://[^"']+\.cdninstagram\.com[^"']+\.mp4[^"']*`,
	}
	for _, pattern := range patterns {
		r := regexp.MustCompile(pattern)
		result := r.FindStringSubmatch(body)
		if len(result) > 1 {
			return normalizeVideoURL(result[1]), nil
		}
		if len(result) == 1 {
			return normalizeVideoURL(result[0]), nil
		}
	}

	return "", errors.New("video not found")
}

func normalizeVideoURL(videoURL string) string {
	videoURL = strings.TrimSpace(videoURL)
	videoURL = strings.Trim(videoURL, `"'\\`)
	videoURL = html.UnescapeString(videoURL)
	videoURL = strings.ReplaceAll(videoURL, `\/`, `/`)
	videoURL = strings.ReplaceAll(videoURL, `\u0026`, "&")
	videoURL = strings.TrimRight(videoURL, `\`)
	return videoURL
}

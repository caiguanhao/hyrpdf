package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/caiguanhao/baiduocr"
	"github.com/caiguanhao/gotogether"
)

var (
	BaiduOCRAPIKey string

	OCR baiduocr.OCR
)

func debug(a ...interface{}) {
	fmt.Fprintln(os.Stderr, a...)
}

func debugf(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, format, a...)
}

func getPHPSESSID(resp *http.Response) *string {
	for _, cookie := range resp.Cookies() {
		if strings.ToUpper(cookie.Name) == "PHPSESSID" {
			return &cookie.Value
		}
	}
	return nil
}

func solveCaptcha(session *string) (solved, sess *string, err error) {
	var req *http.Request
	req, err = http.NewRequest("GET", "http://www.hengyirong.com/site/captcha/", nil)
	if err != nil {
		return
	}
	if session != nil {
		req.Header.Set("Cookie", "PHPSESSID="+*session)
	}
	client := &http.Client{}
	var resp *http.Response
	resp, err = client.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var image []byte
	image, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var results []string
	results, err = OCR.ParseImage(image)
	if err == nil && len(results) > 0 {
		var pass bool
		pass, err = regexp.MatchString("^[0-9]{4}$", results[0])
		if err != nil {
			return
		} else if pass {
			solved = &(results[0])
			sess = getPHPSESSID(resp)
		}
	}
	if solved == nil {
		err = errors.New("无法破解验证码，重试中")
	}

	return
}

func getDocument(path string, session string) (*goquery.Document, error) {
	req, err := http.NewRequest("GET", "http://www.hengyirong.com"+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", "PHPSESSID="+session)
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return goquery.NewDocumentFromResponse(resp)
}

func postLogin(username, password, captcha, session string) (*string, error) {
	if len(password) > 12 {
		password = password[:12]
	}
	reqBody := url.Values{
		"LoginForm[username]":   {username},
		"LoginForm[password]":   {password},
		"LoginForm[verifyCode]": {captcha},
	}
	req, err := http.NewRequest("POST", "http://www.hengyirong.com/site/login.html", strings.NewReader(reqBody.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	req.Header.Set("cookie", "PHPSESSID="+session)
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	newSess := getPHPSESSID(resp)
	if newSess != nil {
		return newSess, nil
	}
	return nil, errors.New("用户名、密码或验证码错误")
}

func login(username, password string) *string {
	for {
		solved, sess, err := solveCaptcha(nil)
		if err != nil {
			log.Println("login:", err)
			continue
		}
		newSess, err := postLogin(username, password, *solved, *sess)
		if err != nil {
			log.Println("login:", err)
			time.Sleep(time.Second * 2)
			continue
		}
		return newSess
	}
}

func download(remote, local string) (written int64, err error) {
	var resp *http.Response
	resp, err = http.Get(remote)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	err = os.MkdirAll(path.Dir(local), 0755)
	if err != nil {
		return
	}
	_, err = os.Stat(local)
	if err == nil {
		written = -1
		return
	}
	var file *os.File
	file, err = os.Create(local)
	defer file.Close()
	if err == nil {
		written, err = io.Copy(file, resp.Body)
	}
	return
}

func fmtFloat(float float64, suffix string) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.3f", float), "0"), ".") + suffix
}

func humanBytes(bytes float64) string {
	const TB = 1 << 40
	const GB = 1 << 30
	const MB = 1 << 20
	const KB = 1 << 10
	abs := bytes
	if bytes < 0 {
		abs = bytes * -1
	}
	if abs >= TB {
		return fmtFloat(bytes/TB, " TB")
	}
	if abs >= GB {
		return fmtFloat(bytes/GB, " GB")
	}
	if abs >= MB {
		return fmtFloat(bytes/MB, " MB")
	}
	if abs >= KB {
		return fmtFloat(bytes/KB, " KB")
	}
	return fmt.Sprintf("%.0f bytes", bytes)
}

func init() {
	flag.Usage = func() {
		fmt.Println(path.Base(os.Args[0]), "[USER]", "[PASSWORD]")
	}
	flag.Parse()

	if BaiduOCRAPIKey == "" {
		BaiduOCRAPIKey = os.Getenv("BAIDUOCR_APIKEY")
	}
	OCR = baiduocr.OCR{APIKey: BaiduOCRAPIKey}
}

func main() {
	username, password := flag.Arg(0), flag.Arg(1)

	if len(username) == 0 {
		fmt.Print("用户名: ")
		fmt.Scanln(&username)
	}

	if len(password) == 0 {
		fmt.Print("密码: ")
		fmt.Scanln(&password)
	}

	session := login(username, password)

	var records []interface{}

	recordsDoc, err := getDocument("/tender.html", *session)
	if err != nil {
		debug(err)
		return
	}
	recordsDoc.Find(".sx_tab").Each(func(_ int, tab *goquery.Selection) {
		tab.Find("tr").Each(func(index int, tr *goquery.Selection) {
			if index == 0 {
				return
			}
			href, hasHref := tr.Find("a.sx_see").Attr("href")
			if hasHref {
				id := tr.Find("td:first-child").Text()
				records = append(records, [2]string{href, path.Join(username, id)})
			}
		})
	})

	log.Println("找到", len(records), "个记录")

	var details []interface{}

	gotogether.Enumerable(records).Each(func(item interface{}) {
		record := item.([2]string)
		recordDoc, err := getDocument(record[0], *session)
		if err != nil {
			debug(err)
			return
		}
		recordDoc.Find(".H_table_style tr").Each(func(index int, tr *goquery.Selection) {
			if index == 0 {
				return
			}
			href, hasHref := tr.Find("td:last-child a").Attr("href")
			if hasHref {
				name := tr.Find("td:first-child").Text()
				details = append(details, [2]string{href, path.Join(record[1], fmt.Sprintf("%03d.%s", index, name))})
			}
		})
	})

	log.Println("找到", len(details), "个借款人")

	gotogether.Queue{
		Concurrency: 5,
		AddJob: func(jobs *chan interface{}, done *chan interface{}, errs *chan error) {
			gotogether.Enumerable(details).Each(func(item interface{}) {
				detail := item.([2]string)
				detailDoc, err := getDocument(detail[0], *session)
				if err != nil {
					*errs <- err
				} else {
					file, hasFile := detailDoc.Find("iframe").Attr("src")
					if hasFile {
						*jobs <- [2]string{file, detail[1] + ".pdf"}
					}
				}
			})
		},
		OnAddJobError: func(err *error) {
			debug(*err)
		},
		DoJob: func(job *interface{}) (ret interface{}, err error) {
			paths := (*job).([2]string)
			var written int64
			written, err = download(paths[0], paths[1])
			if err != nil {
				return
			}
			ret = []interface{}{paths[1], written, err}
			return
		},
		OnJobError: func(err *error) {
			debug(*err)
		},
		OnJobSuccess: func(ret *interface{}) {
			rets := (*ret).([]interface{})
			file, written := rets[0].(string), rets[1].(int64)
			if written < 0 {
				debug(file, "已存在")
			} else {
				debug(file, "已下载", humanBytes(float64(written)))
			}
		},
	}.Run()

	fmt.Println("已完成，可以关闭了。")
	time.Sleep(time.Minute * 5)
}

package secKill

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/axgle/mahonia"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/tidwall/gjson"
	"github.com/zqijzqj/mtSecKill/chromedpEngine"
	"github.com/zqijzqj/mtSecKill/global"
	"github.com/zqijzqj/mtSecKill/logs"
)

type jdSecKill struct {
	ctx         context.Context
	cancel      context.CancelFunc
	bCtx        context.Context
	bWorksCtx   []context.Context
	isLogin     bool
	isClose     bool
	mu          sync.Mutex
	userAgent   string
	UserInfo    gjson.Result
	SkuId       string
	SecKillUrl  string
	SecKillNum  int
	SecKillInfo gjson.Result
	eid         string
	fp          string
	Works       int
	IsOkChan    chan struct{}
	IsOk        bool
	StartTime   time.Time
	DiffTime    int64
	clickCount  int64 // 新增点击计数器
	openURL     string
	dom         string
}

func NewJdSecKill(execPath string, skuId string, num, works int, openURL string, dom string) *jdSecKill {
	if works < 0 {
		works = 1
	}
	jsk := &jdSecKill{
		ctx:        nil,
		isLogin:    false,
		isClose:    false,
		userAgent:  chromedpEngine.GetRandUserAgent(),
		SkuId:      skuId,
		SecKillNum: num,
		Works:      works,
		IsOk:       false,
		IsOkChan:   make(chan struct{}, 1),
		openURL:    "",
		dom:        "",
	}
	jsk.ctx, jsk.cancel = chromedpEngine.NewExecCtx(chromedp.ExecPath(execPath), chromedp.UserAgent(jsk.userAgent))
	jsk.openURL, jsk.dom = openURL, dom
	return jsk
}

func (jsk *jdSecKill) Stop() {
	jsk.mu.Lock()
	defer jsk.mu.Unlock()
	if jsk.isClose {
		return
	}
	jsk.isClose = true
	c := jsk.cancel
	c()
	return
}

func (jsk *jdSecKill) GetReq(reqUrl string, params map[string]string, referer string, ctx context.Context) (gjson.Result, error) {
	if referer == "" {
		referer = "https://www.jd.com"

	}
	if ctx == nil {
		ctx = jsk.bCtx
	}
	req, _ := http.NewRequest("GET", reqUrl, nil)
	req.Header.Add("User-Agent", jsk.userAgent)
	req.Header.Add("Referer", referer)
	req.Header.Add("Host", req.URL.Host)
	if len(params) > 0 {
		q := req.URL.Query()
		for k, v := range params {
			q.Add(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}
	resp, err := chromedpEngine.RequestByCookie(ctx, req)
	if err != nil {
		return gjson.Result{}, err
	}
	defer resp.Body.Close()
	b, _ := ioutil.ReadAll(resp.Body)
	logs.PrintlnSuccess("Get请求接口:", req.URL)
	//	logs.PrintlnSuccess(string(b))
	logs.PrintlnInfo("=======================")
	r := FormatJdResponse(b, req.URL.String(), false)
	if r.Raw == "null" || r.Raw == "" {
		return gjson.Result{}, errors.New("获取数据失败：" + r.Raw)
	}
	return r, nil
}

func (jsk *jdSecKill) SyncJdTime() {
	resp, _ := http.Get("https://a.jd.com//ajax/queryServerData.html")
	defer resp.Body.Close()
	b, _ := ioutil.ReadAll(resp.Body)
	r := gjson.ParseBytes(b)
	jdTimeUnix := r.Get("serverTime").Int()
	jsk.DiffTime = global.UnixMilli() - jdTimeUnix
	logs.PrintlnInfo("服务器与本地时间差为: ", jsk.DiffTime, "ms")
}

func (jsk *jdSecKill) PostReq(reqUrl string, params url.Values, referer string, ctx context.Context) (gjson.Result, error) {
	if ctx == nil {
		ctx = jsk.bCtx
	}
	req, _ := http.NewRequest("POST", reqUrl, strings.NewReader(params.Encode()))
	req.Header.Add("User-Agent", jsk.userAgent)
	if referer != "" {
		req.Header.Add("Referer", referer)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Add("Host", req.URL.Host)
	resp, err := chromedpEngine.RequestByCookie(ctx, req)
	if err != nil {
		return gjson.Result{}, err
	}
	defer resp.Body.Close()
	b, _ := ioutil.ReadAll(resp.Body)

	logs.PrintlnSuccess("Post请求连接", req.URL)
	logs.PrintlnInfo("=======================")
	r := FormatJdResponse(b, req.URL.String(), false)
	if r.Raw == "null" || r.Raw == "" {
		return gjson.Result{}, errors.New("获取数据失败：" + r.Raw)
	}
	return r, nil
}

func FormatJdResponse(b []byte, prefix string, isConvertStr bool) gjson.Result {
	r := string(b)
	if isConvertStr {
		r = mahonia.NewDecoder("gbk").ConvertString(r)
	}
	r = strings.TrimSpace(r)
	if prefix != "" {
		//这里针对http连接 自动提取jsonp的callback
		if strings.HasPrefix(prefix, "http") {
			pUrl, err := url.Parse(prefix)
			if err == nil {
				prefix = pUrl.Query().Get("callback")
			}
		}
		r = strings.TrimPrefix(r, prefix)
	}
	if strings.HasSuffix(r, ")") {
		r = strings.TrimLeft(r, `(`)
		r = strings.TrimRight(r, ")")
	}
	return gjson.Parse(r)
}

// 初始化监听请求数据
func (jsk *jdSecKill) InitActionFunc() chromedp.ActionFunc {
	return func(ctx context.Context) error {
		jsk.bCtx = ctx
		_ = network.Enable().Do(ctx)
		chromedp.ListenTarget(ctx, func(ev interface{}) {
			switch e := ev.(type) {
			// 监听链接接口返回是否
			case *network.EventResponseReceived:
				go func() {
					if strings.Contains(e.Response.URL, "passport.jd.com/user/petName/getUserInfoForMiniJd.action") {
						b, err := network.GetResponseBody(e.RequestID).Do(ctx)
						if err == nil {
							jsk.UserInfo = FormatJdResponse(b, e.Response.URL, false)
						}
						jsk.isLogin = true
					}
				}()

			}
		})
		return nil
	}
}

// 执行代码
func (jsk *jdSecKill) Run() error {
	return chromedp.Run(jsk.ctx, chromedp.Tasks{

		jsk.InitActionFunc(),
		chromedp.Navigate("https://passport.jd.com/uc/login"),
		chromedp.ActionFunc(func(ctx context.Context) error {
			logs.PrintlnInfo("等待登陆......")
			for {
				select {
				case <-jsk.ctx.Done():
					logs.PrintErr("浏览器被关闭，退出进程")
					return nil
				case <-jsk.bCtx.Done():
					logs.PrintErr("浏览器被关闭，退出进程")
					return nil
				default:
				}
				if jsk.isLogin {
					logs.PrintlnSuccess(jsk.UserInfo.Get("realName").String() + ", 登陆成功........")
					break
				}
			}
			return nil
		}),
		jsk.NavigatePage(),
		jsk.WaitStart(),
		jsk.PollListen(),
	})
}

func (jsk *jdSecKill) NavigatePage() chromedp.ActionFunc {
	return func(ctx context.Context) error {
		logs.PrintlnInfo("加载页面....")
		chromedp.Navigate(jsk.openURL).Do(ctx)
		time.Sleep(1 * time.Second)
		var s bool
		chromedp.Evaluate(`
             (function() {
     			window.scrollTo({top: 3000, behavior: 'smooth'})
            `, &s).Do(ctx)

		return nil

	}
}

func (jsk *jdSecKill) WaitStart() chromedp.ActionFunc {
	return func(ctx context.Context) error {

	RE:
		st := jsk.StartTime.UnixNano() / 1e6
		logs.PrintlnInfo("等待时间到达" + jsk.StartTime.Format(global.DateTimeFormatStr) + "...... 请勿关闭浏览器")

		aa := global.UnixMilli()
		if aa-st >= -100 {
			logs.PrintlnInfo("时间到达。。。。开始执行")
			return nil
		} else {

			logs.PrintlnInfo((aa - st))

			if aa-st > -1000 {
				time.Sleep(100 * time.Microsecond)
				goto RE
			} else {
				// 间隔 1 秒刷新
				time.Sleep(1 * time.Second)
			}
			goto RE
		}
	}
}

func (jsk *jdSecKill) PollListen() chromedp.ActionFunc {
	return func(ctx context.Context) error {

		for {
			logs.PrintlnInfo("发现可点按钮")

			err := chromedp.WaitVisible(jsk.dom, chromedp.ByQuery).Do(ctx)
			if err != nil {
				return fmt.Errorf("等待按钮显示失败: %w", err)
			}
			_ = chromedp.Click(jsk.dom, chromedp.ByQuery).Do(ctx)
			time.Sleep(1 * time.Nanosecond)

			logs.PrintlnInfo("点击完成")
		}

	}

}

// // 执行实际的点击操作
// func (jsk *jdSecKill) doClick(ctx context.Context) error {
// 	// 使用短超时上下文
// 	clickCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
// 	defer cancel()

// 	// 点击操作任务组
// 	tasks := chromedp.Tasks{
// 		chromedp.ActionFunc(func(ctx context.Context) error {
// 			// 使用JavaScript检查元素可见性
// 			var visible bool
// 			err := chromedp.Evaluate(`
//                 (function() {
//                     const el = document.querySelector(`+"`"+jsk.dom+"`"+`);

// 					const visible = el && el.offsetParent !== null
// 					if (visible) {
// 						el.scrollIntoView()
// 					}
//                     return visible;
//                 })()
//             `, &visible).Do(ctx)

// 			if err != nil || !visible {

// 				return fmt.Errorf("button not visible")
// 			}
// 			return nil
// 		}),
// 		chromedp.Click(jsk.dom, chromedp.ByQuery, chromedp.NodeVisible),
// 	}

// 	// 执行点击
// 	err := chromedp.Run(clickCtx, tasks)
// 	if err != nil {
// 		if !strings.Contains(err.Error(), "button not visible") {
// 			logs.PrintlnInfo("点击异常:", err)
// 		}
// 		return err
// 	}

// 	// 记录点击成功
// 	atomic.AddInt64(&jsk.clickCount, 1)
// 	return nil
// }

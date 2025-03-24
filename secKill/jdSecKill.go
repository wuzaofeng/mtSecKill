package secKill

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/axgle/mahonia"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
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
}

func NewJdSecKill(execPath string, skuId string, num, works int) *jdSecKill {
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
	}
	jsk.ctx, jsk.cancel = chromedpEngine.NewExecCtx(chromedp.ExecPath(execPath), chromedp.UserAgent(jsk.userAgent))
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
		jsk.WaitStart(),
		jsk.PollListen(),
		// chromedp.ActionFunc(func(ctx context.Context) error {
		// 	jsk.GetEidAndFp()
		// 	//提取抢购连接
		// 	// for _, v := range jsk.bWorksCtx {
		// 	// 	go func(ctx2 context.Context) {
		// 	// 		// jsk.FetchSecKillUrl()
		// 	// 		logs.PrintlnInfo("跳转链接......")

		// 	// 		// _ = chromedp.Navigate("https://pro.m.jd.com/mall/active/3HVGeaYAqEaSPUpCRDt3BBgGhioB/index.html?_ts=1742375471386&utm_user=plusmember&gx=RnAomTM2FlSdt9tM280OJ6eeDGMvFA&gxd=RnAoxGdaYTfZw8xG-tZwWbrg6hMG75g&ad_od=share&baseType=3&uabt=613_7363_1_0&hideyl=1&cu=true&utm_source=lianmeng__2__weibo.com&utm_medium=jingfen&utm_campaign=t_1001701389_2011078412_4100072861_3003030792&utm_term=7b8e3ede95b7475d8ee81c404384870b").Do(ctx)
		// 	// 		// logs.PrintlnInfo("等待页面更新完成....")
		// 	// 		// _ = chromedp.WaitVisible(".coupon_wrapper").Do(ctx)
		// 	// 		// var itemNodes []*cdp.Node
		// 	// 		// err := chromedp.Nodes(".coupon_wrapper", &itemNodes, chromedp.ByQueryAll).Do(ctx)
		// 	// 		err := chromedp.Click(".pane-switch__item-btn").Do(ctx)

		// 	// 		logs.PrintlnInfo("点击完成")

		// 	// 		// chromedp.Click(".coupon_wrapper").Do(ctx)
		// 	// 		// 	_, _, _, _ = page.Navigate(jsk.SecKillUrl).WithReferrer("https://item.jd.com/" + jsk.SkuId + ".html").Do(ctx2)
		// 	// 	SecKillRE:

		// 	// 		//请求抢购连接，提交订单
		// 	// 		// err := jsk.ReqSubmitSecKillOrder(ctx2)
		// 	// 		if err != nil {
		// 	// 			logs.PrintlnInfo(err, "等待重试")
		// 	// 			goto SecKillRE
		// 	// 		}
		// 	// 	}(v)
		// 	// }
		// 	// select {
		// 	// case <-jsk.IsOkChan:
		// 	// 	logs.PrintlnInfo("抢购成功。。。10s后关闭进程...")
		// 	// 	_ = chromedp.Sleep(10 * time.Second).Do(ctx)
		// 	// case <-jsk.ctx.Done():
		// 	// case <-jsk.bCtx.Done():
		// 	// }
		// 	return nil
		// }),
	})
}

func (jsk *jdSecKill) WaitStart() chromedp.ActionFunc {
	return func(ctx context.Context) error {
		// u := "https://item.jd.com/" + jsk.SkuId + ".html"
		// for i := 0; i < jsk.Works; i++ {
		// 	go func() {
		// 		tid, err := target.CreateTarget(u).Do(ctx)
		// 		if err == nil {
		// 			c, _ := chromedp.NewContext(jsk.bCtx, chromedp.WithTargetID(tid))
		// 			_ = chromedp.Run(c, chromedp.Tasks{
		// 				chromedp.ActionFunc(func(ctx context.Context) error {
		// 					logs.PrintlnInfo("打开新的抢购标签.....")
		// 					jsk.mu.Lock()
		// 					jsk.bWorksCtx = append(jsk.bWorksCtx, ctx)
		// 					jsk.mu.Unlock()
		// 					return nil
		// 				}),
		// 			})
		// 		}
		// 	}()
		// }
		// _ = chromedp.Navigate(u).Do(ctx)
	RE:
		st := jsk.StartTime.UnixNano() / 1e6
		logs.PrintlnInfo("等待时间到达" + jsk.StartTime.Format(global.DateTimeFormatStr) + "...... 请勿关闭浏览器")
		// for {
		// 	select {
		// 	case <-jsk.ctx.Done():
		// 		logs.PrintErr("浏览器被关闭，退出进程")
		// 		return nil
		// 	case <-jsk.bCtx.Done():
		// 		logs.PrintErr("浏览器被关闭，退出进程")
		// 		return nil
		// 	default:
		// 	}

		aa := global.UnixMilli()
		if aa-st >= 0 {
			logs.PrintlnInfo("时间到达。。。。开始执行")
			return nil
		} else {

			logs.PrintlnInfo((aa - st))

			if aa-st > -2000 {
				time.Sleep(1 * time.Microsecond)
				goto RE
			} else {
				// 间隔 1 秒刷新
				time.Sleep(1 * time.Second)
			}
			goto RE
		}
		// }
		// return nil
	}
}

func (jsk *jdSecKill) GetEidAndFp() chromedp.ActionFunc {
	return func(ctx context.Context) error {

		// RE:
		for { // 使用无限循环替代 goto
			logs.PrintlnInfo("加载页面....")
			err := chromedp.Navigate("https://pro.m.jd.com/mall/active/3HVGeaYAqEaSPUpCRDt3BBgGhioB/index.html?_ts=1742375471386&utm_user=plusmember&gx=RnAomTM2FlSdt9tM280OJ6eeDGMvFA&gxd=RnAoxGdaYTfZw8xG-tZwWbrg6hMG75g&ad_od=share&baseType=3&uabt=613_7363_1_0&hideyl=1&cu=true&utm_source=lianmeng__2__weibo.com&utm_medium=jingfen&utm_campaign=t_1001701389_2011078412_4100072861_3003030792&utm_term=7b8e3ede95b7475d8ee81c404384870b").Do(ctx)

			if err != nil {
				return fmt.Errorf("页面导航失败: %w", err)
			}
			logs.PrintlnInfo("等待页面完成....")

			logs.PrintlnInfo("监听按钮显示....")

			err = chromedp.WaitVisible(".button_no_click").Do(ctx)
			if err != nil {
				return fmt.Errorf("等待按钮显示失败: %w", err)
			}
			// _ = chromedp.ActionFunc(func(ctx context.Context) error {}).Do(ctx)
			// // var itemNodes []*cdp.Node
			// // err := chromedp.Nodes(".coupon_wrapper", &itemNodes, chromedp.ByQueryAll).Do(ctx)

			logs.PrintlnInfo("发现可点按钮")
			_ = chromedp.WaitVisible(".button_can_click").Do(ctx)
			_ = chromedp.Click(".coupon_wrapper").Do(ctx)

			logs.PrintlnInfo("点击完成")

			time.Sleep(1 * time.Microsecond)
			// goto RE

		}
		return nil
	}
}

func (jsk *jdSecKill) PollListen() chromedp.ActionFunc {
	return func(ctx context.Context) error {
		for { // 主循环持续监控
			// 同时监听两种按钮状态
			var (
				noClickVisible  bool
				canClickVisible bool
			)

			logs.PrintlnInfo("加载页面....")
			err := chromedp.Navigate("https://pro.m.jd.com/mall/active/3HVGeaYAqEaSPUpCRDt3BBgGhioB/index.html?_ts=1742375471386&utm_user=plusmember&gx=RnAomTM2FlSdt9tM280OJ6eeDGMvFA&gxd=RnAoxGdaYTfZw8xG-tZwWbrg6hMG75g&ad_od=share&baseType=3&uabt=613_7363_1_0&hideyl=1&cu=true&utm_source=lianmeng__2__weibo.com&utm_medium=jingfen&utm_campaign=t_1001701389_2011078412_4100072861_3003030792&utm_term=7b8e3ede95b7475d8ee81c404384870b").Do(ctx)

			if err != nil {
				return fmt.Errorf("页面导航失败: %w", err)
			}
			logs.PrintlnInfo("等待页面完成....")

			// 使用 Poll 轮询检测按钮状态
			err = chromedp.Run(ctx,
				chromedp.Tasks{
					// 并行检测两种按钮
					chromedp.EvaluateAsDevTools(`
                            document.querySelector('.free_coupon_module.coupon_1x .button_no_click') !== null 
                            && document.querySelector('.free_coupon_module.coupon_1x .button_no_click').offsetParent !== null
                        `, &noClickVisible),
					chromedp.EvaluateAsDevTools(`
                            document.querySelector('.free_coupon_module.coupon_1x .button_can_click') !== null 
                            && document.querySelector('.free_coupon_module.coupon_1x .button_can_click').offsetParent !== null
                        `, &canClickVisible),
				},
			)

			if err != nil {
				return fmt.Errorf("轮询检测失败: %w", err)
			}

			fmt.Print("noClickVisible", noClickVisible)

			fmt.Print("canClickVisible", canClickVisible)

			// 状态处理逻辑
			switch {
			case noClickVisible:
				// 触发页面刷新
				logs.PrintlnInfo("检测到未激活按钮，刷新页面....")

				break
				// if err := chromedp.Reload().Do(ctx); err != nil {
				// 	currentRetry++
				// 	if currentRetry >= maxRetries {
				// 		return fmt.Errorf("页面刷新失败（已达最大重试次数）: %w", err)
				// 	}

				// 	logs.PrintlnInfo("刷新失败，等待重试....")
				// 	time.Sleep(2 * time.Second)
				// } else {
				// 	currentRetry = 0            // 重置计数器
				// 	time.Sleep(1 * time.Second) // 等待页面加载
				// }

			case canClickVisible:
				logs.PrintlnInfo("发现可点击按钮，开始高频触发...")
				// 高频点击（带频率限制）

				// var count := 1
				for {
					logs.PrintlnInfo("发现可点按钮")

					_ = chromedp.Click(".free_coupon_module.coupon_1x .button_can_click").Do(ctx)
					time.Sleep(1 * time.Nanosecond)
					logs.PrintlnInfo("点击完成")
				}

			default:
				logs.PrintlnInfo("未检测到目标按钮，等待状态变化...")
				time.Sleep(1 * time.Second)
			}
		}
		return nil
	}
}

// 辅助函数：检测元素是否存在
func elementExists(ctx context.Context, selector string) (bool, error) {
	var exists bool
	err := chromedp.EvaluateAsDevTools(
		fmt.Sprintf(`document.querySelector('%s') !== null`, selector),
		&exists,
	).Do(ctx)
	return exists, err
}

func (jsk *jdSecKill) FetchSecKillUrl() {
	logs.PrintlnInfo("开始获取抢购连接.....")
	for {
		if jsk.SecKillUrl != "" {
			break
		}
		jsk.SecKillUrl = jsk.GetSecKillUrl()
		logs.PrintlnWarning("抢购链接获取失败.....正在重试")
	}
	jsk.SecKillUrl = "https:" + strings.TrimPrefix(jsk.SecKillUrl, "https:")
	jsk.SecKillUrl = strings.ReplaceAll(jsk.SecKillUrl, "divide", "marathon")
	jsk.SecKillUrl = strings.ReplaceAll(jsk.SecKillUrl, "user_routing", "captcha.html")
	logs.PrintlnSuccess("抢购连接获取成功....", jsk.SecKillUrl)
	return
}

func (jsk *jdSecKill) ReqSubmitSecKillOrder(ctx context.Context) error {
	if ctx == nil {
		ctx = jsk.bCtx
	}
	//这里直接使用浏览器跳转 主要目的是获取cookie
	skUrl := fmt.Sprintf("https://marathon.jd.com/seckill/seckill.action?=skuId=%s&num=%d&rid=%d", jsk.SkuId, jsk.SecKillNum, time.Now().Unix())
	logs.PrintlnInfo("访问抢购订单结算页面......", skUrl)
	_, _, _, _ = page.Navigate(skUrl).WithReferrer("https://item.jd.com/" + jsk.SkuId + ".html").Do(ctx)

	logs.PrintlnInfo("获取抢购信息...............")
	for {
		err := jsk.GetSecKillInitInfo(ctx)
		if err != nil {
			logs.PrintErr("抢购失败：", err, "正在重试.......")
		}
		break
	}
	orderData := jsk.GetOrderReqData()

	if len(orderData) == 0 {
		return errors.New("订单参数生成失败")
	}
	logs.PrintlnInfo("订单参数：", orderData.Encode())
	logs.PrintlnInfo("提交抢购订单.............")
	r, err := jsk.PostReq("https://marathon.jd.com/seckillnew/orderService/pc/submitOrder.action?skuId="+jsk.SkuId+"", orderData, skUrl, ctx)
	if err != nil {
		logs.PrintErr("抢购失败：", err)
	}
	orderId := r.Get("orderId").String()
	if orderId != "" && orderId != "0" {
		jsk.IsOk = true
		jsk.IsOkChan <- struct{}{}
		logs.PrintlnInfo("抢购成功，订单编号:", r.Get("orderId").String())
	} else {
		if r.IsObject() || r.IsArray() {
			return errors.New("抢购失败：" + r.Raw)
		}
		return errors.New("抢购失败,再接再厉")
	}
	return nil
}

func (jsk *jdSecKill) GetOrderReqData() url.Values {
	logs.PrintlnInfo("生成订单所需参数...")
	defer func() {
		if f := recover(); f != nil {
			logs.PrintErr("订单参数错误：", f)
		}
	}()

	addressList := jsk.SecKillInfo.Get("addressList").Array()
	var defaultAddress gjson.Result
	for _, dAddress := range addressList {
		if dAddress.Get("defaultAddress").Bool() {
			logs.PrintlnInfo("获取到默认收货地址")
			defaultAddress = dAddress
		}
	}
	if defaultAddress.Raw == "" {
		logs.PrintlnInfo("没有获取到默认收货地址， 自动选择一个地址")
		defaultAddress = addressList[0]
	}
	invoiceInfo := jsk.SecKillInfo.Get("invoiceInfo")
	r := url.Values{
		"skuId":              []string{jsk.SkuId},
		"num":                []string{strconv.Itoa(jsk.SecKillNum)},
		"addressId":          []string{defaultAddress.Get("id").String()},
		"yuShou":             []string{"true"},
		"isModifyAddress":    []string{"false"},
		"name":               []string{defaultAddress.Get("name").String()},
		"provinceId":         []string{defaultAddress.Get("provinceId").String()},
		"cityId":             []string{defaultAddress.Get("cityId").String()},
		"countyId":           []string{defaultAddress.Get("countyId").String()},
		"townId":             []string{defaultAddress.Get("townId").String()},
		"addressDetail":      []string{defaultAddress.Get("addressDetail").String()},
		"mobile":             []string{defaultAddress.Get("mobile").String()},
		"mobileKey":          []string{defaultAddress.Get("mobileKey").String()},
		"email":              []string{defaultAddress.Get("email").String()},
		"postCode":           []string{""},
		"invoiceTitle":       []string{""},
		"invoiceCompanyName": []string{""},
		"invoiceContent":     []string{},
		"invoiceTaxpayerNO":  []string{""},
		"invoiceEmail":       []string{""},
		"invoicePhone":       []string{invoiceInfo.Get("invoicePhone").String()},
		"invoicePhoneKey":    []string{invoiceInfo.Get("invoicePhoneKey").String()},
		"invoice":            []string{"true"},
		"password":           []string{""},
		"codTimeType":        []string{"3"},
		"paymentType":        []string{"4"},
		"areaCode":           []string{""},
		"overseas":           []string{"0"},
		"phone":              []string{""},
		"eid":                []string{jsk.eid},
		"fp":                 []string{jsk.fp},
		"token":              []string{jsk.SecKillInfo.Get("token").String()},
		"pru":                []string{""},
	}

	t := invoiceInfo.Get("invoiceTitle").String()
	if t != "" {
		r["invoiceTitle"] = []string{t}
	} else {
		r["invoiceTitle"] = []string{"-1"}
	}

	t = invoiceInfo.Get("invoiceContentType").String()
	if t != "" {
		r["invoiceContent"] = []string{t}
	} else {
		r["invoiceContent"] = []string{"1"}
	}

	return r
}

func (jsk *jdSecKill) GetSecKillInitInfo(ctx context.Context) error {
	r, err := jsk.PostReq("https://marathon.jd.com/seckillnew/orderService/pc/init.action", url.Values{
		"sku":             []string{jsk.SkuId},
		"num":             []string{strconv.Itoa(jsk.SecKillNum)},
		"isModifyAddress": []string{"false"},
	}, fmt.Sprintf("https://marathon.jd.com/seckill/seckill.action?=skuId=100012043978&num=2&rid=%d6", time.Now().Unix()), ctx)
	if err != nil {
		return err
	}
	jsk.SecKillInfo = r
	logs.PrintlnInfo("秒杀信息获取成功：", jsk.SecKillInfo.Raw)
	return nil
}

func (jsk *jdSecKill) GetSecKillUrl() string {
	req, _ := http.NewRequest("GET", "https://itemko.jd.com/itemShowBtn", nil)
	req.Header.Add("User-Agent", jsk.userAgent)
	req.Header.Add("Referer", "https://item.jd.com/"+jsk.SkuId+".html")
	r, _ := jsk.GetReq("https://itemko.jd.com/itemShowBtn", map[string]string{
		"callback": "jQuery" + strconv.FormatInt(global.GenerateRangeNum(1000000, 9999999), 10),
		"skuId":    jsk.SkuId,
		"from":     "pc",
		"_":        strconv.FormatInt(time.Now().Unix()*1000, 10),
	}, "https://item.jd.com/"+jsk.SkuId+".html", nil)
	return r.Get("url").String()
}

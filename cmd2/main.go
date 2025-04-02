package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/proxy"
	"golang.org/x/time/rate"
)

type Config struct {
	TargetURL     string            `json:"target_url"`
	TotalRequests int               `json:"total_requests"`
	ExecuteTime   string            `json:"execute_time"`
	Headers       map[string]string `json:"headers"`
	Cookies       string            `json:"cookies"`
	PostData      string            `json:"post_data"`
	RateLimit     RateConfig        `json:"rate_limit"`
	Proxies       []ProxyConfig     `json:"proxies"`
	ProxyMode     string            `json:"proxy_mode"` // random or round_robin
}

type RateConfig struct {
	Enabled     bool `json:"enabled"`
	RequestsNum int  `json:"requests_num"`
	TimeSeconds int  `json:"time_seconds"`
}

type ProxyConfig struct {
	URL      string `json:"url"`
	Type     string `json:"type"` // http or socks5
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type Response struct {
	RequestID    int
	StatusCode   int
	ResponseBody string
	Error        error
	Duration     time.Duration
	ProxyUsed    string
}

type Stats struct {
	successCount  int32
	failCount     int32
	totalDuration time.Duration
	minDuration   time.Duration
	maxDuration   time.Duration
	statusCodes   map[int]int32
	errorMessages map[string]int32
	proxyStats    map[string]int32
	mu            sync.Mutex
}

type ProxySelector struct {
	proxies    []ProxyConfig
	mode       string
	currentIdx uint32
	mu         sync.Mutex
}

var (
	config Config
	stats  Stats
)

func initDefaultConfig() {
	config = Config{
		TargetURL:     "https://api.m.jd.com/client.action?functionId=newBabelAwardCollection",
		TotalRequests: 3000,
		ExecuteTime:   "17:59:59",

		PostData: `body=%7B%22activityId%22%3A%224HfnzzR9fEiGxYamWbf65PnPj9WD%22%2C%22gridInfo%22%3A%22%22%2C%22transParam%22%3A%22%7B%5C%22bsessionId%5C%22%3A%5C%226c72f973-3743-4aff-874d-e2d3d646a840%5C%22%2C%5C%22babelChannel%5C%22%3A%5C%22%5C%22%2C%5C%22actId%5C%22%3A%5C%2201845934%5C%22%2C%5C%22enActId%5C%22%3A%5C%224HfnzzR9fEiGxYamWbf65PnPj9WD%5C%22%2C%5C%22pageId%5C%22%3A%5C%225444074%5C%22%2C%5C%22encryptCouponFlag%5C%22%3A%5C%221%5C%22%2C%5C%22requestChannel%5C%22%3A%5C%22h5%5C%22%2C%5C%22jdAtHomePage%5C%22%3A%5C%220%5C%22%2C%5C%22utmFlag%5C%22%3A%5C%220%5C%22%2C%5C%22locType%5C%22%3A%5C%221%5C%22%7D%22%2C%22scene%22%3A%221%22%2C%22args%22%3A%22key%3D05B0E079770AB36FD9A68EB6DD4D0AB515318F78945D297CECC81E8E4D445074BBA9D2EC1EAD82121979DF1C615E7C64_bingo%2CroleId%3DF840E5D973FD2AAA5BC933D0F9B25256E9111BA172589D85B5C79F34D554AC54B97C64250ADE2E4CD5F0A4868DCFB7D805D7F8516397D1971EC9583A16B0F97911F450856106F5AD6EB8273BAEA0C496ECEAA6CE1313D7C85D9AEA3A82A2E62A8D4783BDB164CEA5F5643ACAAA8ED3EE9648D4EBFA93BDF3A724A7FEDD9417F73B54B369F781A9BFCC333D254C2403E4CE9A1E7A95496DDE2E904E4BFDBA845F_bingo%2CstrengthenKey%3D19F512DCD8EE34ABE9C4FB4A92C2F42AE8B0925BF37D2F1D8C149CDA1E70F74C_bingo%22%2C%22platform%22%3A%221%22%2C%22orgType%22%3A%222%22%2C%22openId%22%3A%22-1%22%2C%22pageClickKey%22%3A%22-1%22%2C%22eid%22%3A%226EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPM%22%2C%22fp%22%3A%22558493836a7258e063320b61ead00b02%22%2C%22shshshfp%22%3A%22bcba984fc4772288df465c4e94c45b7b%22%2C%22shshshfpa%22%3A%22ec986d0e-c4b3-fc55-a223-9798c850ffe6-1743494491%22%2C%22shshshfpb%22%3A%22BApXSOOvJ8_BA9vRb7RtqYPXTe4nPq-VSlanhz4-V9xJ1Muam9oG2%22%2C%22childActivityUrl%22%3A%22https%253A%252F%252Fh5static.m.jd.com%252Fmall%252Factive%252F4HfnzzR9fEiGxYamWbf65PnPj9WD%252Findex.html%253Futm_user%253Dplusmember%2526_ts%253D1742819243430%2526ad_od%253Dshare%2526gxd%253DRnAowmYKPGXfnp4Sq4B_W578vOMp4E7JgUugKDcomXTOIlSPI-BCnvuytD0G7kc%2526gx%253DRnAomTM2PUO_ss8T04FzPCuSv0HqkkASPQ%2526PTAG%253D17053.1.1%2526cu%253Dtrue%2526utm_source%253Dlianmeng__9__kong%2526utm_medium%253Djingfen%2526utm_campaign%253Dt_1001701389_2011078412_4100072861_3003030792%2526utm_term%253Dbc18474378e54dc89d83c40414e3d491%2526preventPV%253D1%2526forceCurrentView%253D1%22%2C%22userArea%22%3A%22-1%22%2C%22client%22%3A%22-1%22%2C%22clientVersion%22%3A%22-1%22%2C%22uuid%22%3A%22-1%22%2C%22osVersion%22%3A%22-1%22%2C%22brand%22%3A%22-1%22%2C%22model%22%3A%22-1%22%2C%22networkType%22%3A%22-1%22%2C%22jda%22%3A%22122270672.1743494489941124857168.1743494489.1743494489.1743500930.2%22%2C%22jsToken%22%3A%22jdd036EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPMAAAAMV6DAPDWIAAAAAC4AV4EUAP5GUSYX%22%2C%22sdkToken%22%3Anull%2C%22pageClick%22%3A%22Babel_Coupon%22%2C%22couponSource%22%3A%22manual%22%2C%22couponSourceDetail%22%3A%22-100%22%2C%22channel%22%3A%22%E9%80%9A%E5%A4%A9%E5%A1%94%E4%BC%9A%E5%9C%BA%22%2C%22batchId%22%3A%221150570894%22%2C%22headArea%22%3A%22%22%2C%22couponTemplateFrom%22%3A%22%22%2C%22mitemAddrId%22%3A%22%22%2C%22geo%22%3A%7B%22lng%22%3A%22%22%2C%22lat%22%3A%22%22%7D%2C%22addressId%22%3A%22%22%2C%22posLng%22%3A%22%22%2C%22posLat%22%3A%22%22%2C%22un_area%22%3A%22%22%2C%22jdv%22%3A%22lianmeng__9__kong%7Ct_1001701389_2011078412_4100072861_3003030792%7Cjingfen%7Cbc18474378e54dc89d83c40414e3d491%22%2C%22focus%22%3A%22%22%2C%22innerAnchor%22%3A%22%22%2C%22cv%22%3A%222.0%22%2C%22gLng1%22%3A%22%22%2C%22gLat1%22%3A%22%22%2C%22head_area%22%3A%22%22%2C%22receiverLng%22%3A%22%22%2C%22receiverLat%22%3A%22%22%2C%22fullUrl%22%3A%22https%3A%2F%2Fh5static.m.jd.com%2Fmall%2Factive%2F4HfnzzR9fEiGxYamWbf65PnPj9WD%2Findex.html%3Futm_user%3Dplusmember%26_ts%3D1742819243430%26ad_od%3Dshare%26gxd%3DRnAowmYKPGXfnp4Sq4B_W578vOMp4E7JgUugKDcomXTOIlSPI-BCnvuytD0G7kc%26gx%3DRnAomTM2PUO_ss8T04FzPCuSv0HqkkASPQ%26PTAG%3D17053.1.1%26cu%3Dtrue%26utm_source%3Dlianmeng__9__kong%26utm_medium%3Djingfen%26utm_campaign%3Dt_1001701389_2011078412_4100072861_3003030792%26utm_term%3Dbc18474378e54dc89d83c40414e3d491%26preventPV%3D1%26forceCurrentView%3D1%22%2C%22log%22%3A%221743500973309~1RM9eyJsuFwMDFCS1FBTzk5MQ%3D%3D.c3xlcnpye2h3eHR%2BZT8KGn4GDwwxeid4MXNnZ21%2BbnovczFzNRMAPxoYHg45CHMOAw57PQMjeBA%2FIBgfGh80dSESOnwXHC4qPyk1dmYHeDcIehw0Li9yPgZ9PDU%3D.12e367ea~1%2C1~1745BA9622A8976B382D634BA7755493E6420A65~1dzrxuv~C~TBFEVBALbBFUCh9kdB9%2Fbh4FZAMcWB5FFW4cG0ZfWhEKYhBVBR5leh5%2BYB8Ea2YdVh9EGx4TUwcdbHEdeGQcDWZhG1IcTRAdFVcBFGdyG3xnFQZkBx9FFUYTah8SXkBfFQkBFRBCRBEKGwMGAwAFCQIIAQQCAAcHBQIJGx4TQFZUGwgTQ0dEX1RFUVUSFRBGUlISAxBXUUdETEZEVhEcG0JVWREKYgcAGwcBDh4DDx8IFQEdB24cG1hbFQkBFRBSRBEKG1NXVgMDWgZUAAIAWAUAAlcHXAFVVgAFXVFVAwcAAVcHFR8SV0ITDRFnUFwCBREcG0YTDQIHDQEEBwMJDgECBwocG1haFQkSWBAdFVVAWxALFXFxSWpiXUtScwpudXJSSn9UeUVhagZjc0JvTAtLW1gEAXBWcnphDgZkDkt6CGIFB3p7XVd%2BTlh7DQN5BUd0c2h7XVkFXwNWFR8SV0QTDRF3Vl1WW1YQcFxSGREcG1xQQREKG1ETGxFDWkATDWgBDQYBGwEFDwJsGxFCVhALbBFRGx4TVhEcG1MTGxFRGx4TVhEcG1MTGxFRG28dFVpfWBALFVVWX1RXUUdEGx4TVlkSAxBEFR8SWlsTDRFHChwEGQESFRBSUWxGGwgTDgoSFRBTUxEKG0BQWVdfVA9TAnt1DktQRBEcG19bFQlrCB4JBB8BZB4TVV9fXhALFVISFRBcRFQSAxBQFU4%3D~0tcz2bx%22%2C%22random%22%3A%22b5HE7yas%22%2C%22floor_id%22%3A%22115454591%22%7D&screen=1247*1271&client=wh5&clientVersion=1.0.0&sid=&uuid=1743494489941124857168&area=&uemps=&rfs=&xAPIClientLanguage=zh_CN&appid=babelh5&ext=%7B%22sdkToken%22%3Anull%7D&functionId=newBabelAwardCollection&h5st=20250401174934694%3Biawizxp9dqhpwhq2%3B35fa0%3Btk03waa8e1cc618n8VXPLmMfV0mnBqF2IjEWv_W_Ear6NUdtDN2VrHPgGu3Tzl_iNimSG7BULtLzHceodV8SdbnnSDoj%3Beef43870beff9bbeec53b43221aa1e8f%3B5.1%3B1743500973694%3Bt6HsMmbSGRnSGpnV1unQ0J4RNJImOGLm_VImOuMsCWbiOGLmAh4WMusmk_Mm3ebg2Kri2S7WMdbV7ibVIZbi1moV_mriIdri6erVIZLmOGLm_VqTHlYV3lsmOGujMaIVIh4h6m4iJZbW6qLh4ibhIhbi1mbVMl7i3abh7a4iMuMgMiXW41YWLlsmOGuj_uMgMebRMlsmOGujMmLj92ch4xZVCJIVPZrUMuMgMWHmOuMsCmcZBVYUglrUYdpdFlsm0mcT-dITNlHmOuMsCmMi72YUXlsm0mMV_lsmOGujxtsmkmrm0mci9aHWMusmOuMsCKrm0msi9aHWMusmOuMsCObjOGLm8qbRMlsmOusmk_MmhZYS3KJieZodstLmOGLmBxoVApISMusmOuMsCurm0msg5lImOusmOGuj_uMgMSbRMlsmOusmk_sh8uMgMWbRMlsmOusmk_siOGLm5aHWMusmOuMsCurm0msh5lImOusmOGuj7Srm0m8i5lImOusmOGujMaLj92siPZoRF9ImOGLm9aHWMusmOuMsCurm0m8U3lsmOusmk_chOGLm79ImOusmOGuj_uMgM_ImOusmOGuj_uMgMe4RMusmOuMsztMgMeITJdnQJlsmOGujxtsmkmsSPRLh2irg2Obi6ibiMuMgMqrSMusmOuMsztMgMunSMusmk_Mm6WrQOCrh42YUXt8g_2si9usZgt8S3xoVAJ4ZMuMgMqYR7lsmOG_Q%3Ba129955a6cf9224220c964d00229d926&eid=6EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPM&x-api-eid-token=jdd036EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPMAAAAMV6DAPDWIAAAAAC4AV4EUAP5GUSYX`,
		RateLimit: RateConfig{
			Enabled:     true,
			RequestsNum: 1000,
			TimeSeconds: 3,
		},

		// Proxies: []ProxyConfig{
		// 	{
		// 		URL:  "http://220.169.194.49:12666/",
		// 		Type: "http",
		// 	},
		// 	{
		// 		URL:  "http://222.243.174.132:81/",
		// 		Type: "http",
		// 	},
		// 	//  {
		// 	// 	URL:  "https://120.25.199.3:10001/",
		// 	// 	Type: "https",
		// 	// },
		// 	// 添加更多代理
		// },
		// ProxyMode: "round_robin", // 或 "round_robin"
	}

	stats = Stats{
		minDuration:   time.Hour,
		statusCodes:   make(map[int]int32),
		errorMessages: make(map[string]int32),
		proxyStats:    make(map[string]int32),
	}
}

func NewProxySelector(proxies []ProxyConfig, mode string) *ProxySelector {
	return &ProxySelector{
		proxies: proxies,
		mode:    mode,
	}
}

func (ps *ProxySelector) Next() ProxyConfig {
	if len(ps.proxies) == 0 {
		return ProxyConfig{}
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	if ps.mode == "random" {
		return ps.proxies[rand.Intn(len(ps.proxies))]
	}

	// round_robin mode
	idx := atomic.AddUint32(&ps.currentIdx, 1)
	return ps.proxies[(int(idx)-1)%len(ps.proxies)]
}

func createProxyDialer(proxyConfig ProxyConfig) (proxy.Dialer, error) {
	if proxyConfig.Type == "socks5" {
		auth := &proxy.Auth{
			User:     proxyConfig.Username,
			Password: proxyConfig.Password,
		}
		return proxy.SOCKS5("tcp", strings.TrimPrefix(proxyConfig.URL, "socks5://"), auth, proxy.Direct)
	}
	return nil, fmt.Errorf("unsupported proxy type: %s", proxyConfig.Type)
}

func createTransport(proxyConfig ProxyConfig) (*http.Transport, error) {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
	}

	if proxyConfig.Type == "http" {
		proxyURL, err := url.Parse(proxyConfig.URL)
		if err != nil {
			return nil, err
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	} else if proxyConfig.Type == "socks5" {
		dialer, err := createProxyDialer(proxyConfig)
		if err != nil {
			return nil, err
		}
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial(network, addr)
		}
	}

	return transport, nil
}
func makeRequest(client *http.Client, req *http.Request, proxyURL string) Response {
	start := time.Now()
	var response Response
	response.ProxyUsed = proxyURL

	resp, err := client.Do(req)
	if err != nil {
		response.Error = fmt.Errorf("请求失败: %v", err)
		response.Duration = time.Since(start)
		return response
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		response.Error = fmt.Errorf("读取响应失败: %v", err)
		response.Duration = time.Since(start)
		return response
	}

	response.StatusCode = resp.StatusCode
	response.ResponseBody = string(body)
	response.Duration = time.Since(start)
	return response
}

func (s *Stats) update(resp Response) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if resp.Error != nil {
		atomic.AddInt32(&s.failCount, 1)
		s.errorMessages[resp.Error.Error()]++
	} else {
		atomic.AddInt32(&s.successCount, 1)
		s.statusCodes[resp.StatusCode]++
	}

	s.totalDuration += resp.Duration
	if resp.Duration < s.minDuration {
		s.minDuration = resp.Duration
	}
	if resp.Duration > s.maxDuration {
		s.maxDuration = resp.Duration
	}
}

func parseExecuteTime(timeStr string) (time.Time, error) {
	formats := []string{
		"2006-01-02 15:04:05",
		"15:04:05",
		"15:04",
	}

	var targetTime time.Time
	var err error

	for _, format := range formats {
		targetTime, err = time.ParseInLocation(format, timeStr, time.Local)
		if err == nil {
			if format != "2006-01-02 15:04:05" {
				now := time.Now()
				targetTime = time.Date(
					now.Year(), now.Month(), now.Day(),
					targetTime.Hour(), targetTime.Minute(), targetTime.Second(),
					0, time.Local,
				)

				if targetTime.Before(now) {
					targetTime = targetTime.Add(24 * time.Hour)
				}
			}
			return targetTime, nil
		}
	}

	return time.Time{}, fmt.Errorf("不支持的时间格式: %s", timeStr)
}

func main() {
	rand.Seed(time.Now().UnixNano())
	initDefaultConfig()

	// 创建代理选择器
	proxySelector := NewProxySelector(config.Proxies, config.ProxyMode)

	targetTime, err := parseExecuteTime(config.ExecuteTime)
	if err != nil {
		fmt.Printf("解析执行时间失败: %v\n", err)
		return
	}

	logFile, err := os.Create(fmt.Sprintf("request_log_%s.txt",
		time.Now().Format("20060102_150405")))
	if err != nil {
		fmt.Printf("创建日志文件失败: %v\n", err)
		return
	}
	defer logFile.Close()

	fmt.Printf("\n=== 执行配置 ===\n")
	fmt.Printf("目标URL: %s\n", config.TargetURL)
	fmt.Printf("请求次数: %d\n", config.TotalRequests)
	fmt.Printf("目标执行时间: %v\n", targetTime.Format("2006-01-02 15:04:05.000"))
	fmt.Printf("当前系统时间: %v\n", time.Now().Format("2006-01-02 15:04:05.000"))
	fmt.Printf("代理模式: %s\n", config.ProxyMode)
	fmt.Printf("代理数量: %d\n", len(config.Proxies))

	if config.RateLimit.Enabled {
		fmt.Printf("速率限制: %d 请求/%d 秒\n",
			config.RateLimit.RequestsNum,
			config.RateLimit.TimeSeconds)
	}

	// 等待到指定时间
	waitDuration := time.Until(targetTime)
	if waitDuration < 0 {
		fmt.Println("指定的执行时间已过期")
		return
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	fmt.Printf("\n等待执行中...\n")
	for {
		select {
		case <-ticker.C:
			remaining := time.Until(targetTime)
			if remaining <= 0 {
				fmt.Printf("\r开始执行!                                          \n")
				goto START_REQUESTS
			}

			hours := int(remaining.Hours())
			minutes := int(remaining.Minutes()) % 60
			seconds := int(remaining.Seconds()) % 60
			milliseconds := int(remaining.Milliseconds()) % 1000

			fmt.Printf("\r距离执行还剩: %02d:%02d:%02d.%03d",
				hours, minutes, seconds, milliseconds)
		}
	}

START_REQUESTS:
	var limiter *rate.Limiter
	if config.RateLimit.Enabled {
		requestsPerSecond := float64(config.RateLimit.RequestsNum) / float64(config.RateLimit.TimeSeconds)
		limiter = rate.NewLimiter(rate.Limit(requestsPerSecond), config.RateLimit.RequestsNum)
	}

	var wg sync.WaitGroup
	startTime := time.Now()

	fmt.Printf("\n开始发送请求...\n")

	for i := 0; i < config.TotalRequests; i++ {
		wg.Add(1)
		go func(requestID int) {
			defer wg.Done()

			if limiter != nil {
				err := limiter.Wait(context.Background())
				if err != nil {
					fmt.Printf("速率限制等待错误: %v\n", err)
					return
				}
			}

			// 获取代理
			proxyConfig := proxySelector.Next()

			// 创建transport和client
			transport, err := createTransport(proxyConfig)
			if err != nil {
				fmt.Printf("创建transport失败: %v\n", err)
				return
			}

			client := &http.Client{
				Timeout:   30 * time.Second,
				Transport: transport,
			}
			// 创建请求
			req, err := http.NewRequest("POST", config.TargetURL,
				strings.NewReader(config.PostData))
			if err != nil {
				fmt.Printf("创建请求失败: %v\n", err)
				return
			}

			// fmt.Printf("TargetURL: %s\n", config.TargetURL)
			// fmt.Printf("NewReader: %s\n", config.PostData)
			// 设置请求头
			for key, value := range config.Headers {
				req.Header.Set(key, value)
			}
			if config.Cookies != "" {
				req.Header.Set("Cookie", config.Cookies)
			}

			// req, err := http.NewRequest("POST", "https://api.m.jd.com/client.action?functionId=babelGetGuideTips", data)
			req.Header.Set("accept", "*/*")
			req.Header.Set("accept-language", "zh-CN,zh-TW;q=0.9,zh;q=0.8,en-US;q=0.7,en;q=0.6")
			req.Header.Set("cache-control", "no-cache")
			req.Header.Set("content-type", "application/x-www-form-urlencoded")
			req.Header.Set("origin", "https://h5static.m.jd.com")
			req.Header.Set("pragma", "no-cache")
			req.Header.Set("priority", "u=1, i")
			req.Header.Set("referer", "https://h5static.m.jd.com/mall/active/4HfnzzR9fEiGxYamWbf65PnPj9WD/index.html?utm_user=plusmember&_ts=1742819243430&ad_od=share&gxd=RnAowmYKPGXfnp4Sq4B_W578vOMp4E7JgUugKDcomXTOIlSPI-BCnvuytD0G7kc&gx=RnAomTM2PUO_ss8T04FzPCuSv0HqkkASPQ&PTAG=17053.1.1&cu=true&utm_source=lianmeng__9__kong&utm_medium=jingfen&utm_campaign=t_1001701389_2011078412_4100072861_3003030792&utm_term=bc18474378e54dc89d83c40414e3d491&preventPV=1&forceCurrentView=1")
			req.Header.Set("sec-ch-ua", `"Chromium";v="134", "Not:A-Brand";v="24", "Google Chrome";v="134"`)
			req.Header.Set("sec-ch-ua-mobile", "?0")
			req.Header.Set("sec-ch-ua-platform", `"Windows"`)
			req.Header.Set("sec-fetch-dest", "empty")
			req.Header.Set("sec-fetch-mode", "cors")
			req.Header.Set("sec-fetch-site", "same-site")
			req.Header.Set("user-agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36")
			req.Header.Set("x-babel-actid", "4HfnzzR9fEiGxYamWbf65PnPj9WD")
			req.Header.Set("x-referer-page", "https://h5static.m.jd.com/mall/active/4HfnzzR9fEiGxYamWbf65PnPj9WD/index.html")
			req.Header.Set("x-rp-client", "h5_1.0.0")
			req.Header.Set("cookie", "__jdc=122270672; mba_muid=1743494489941124857168; 3AB9D23F7A4B3C9B=6EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPM; b_avif=1; shshshfpa=ec986d0e-c4b3-fc55-a223-9798c850ffe6-1743494491; shshshfpx=ec986d0e-c4b3-fc55-a223-9798c850ffe6-1743494491; autoOpenApp_downCloseDate_auto=1743494491825_1800000; exp-ios-universal=1743498091832; qid_fs=1743494554595; qid_uid=674998aa-94fb-4a46-94e1-5dd5297440ef; qid_evord=2; qid_ls=1743494554597; qid_ts=1743500890997; qid_vis=2; qid_sid=674998aa-94fb-4a46-94e1-5dd5297440ef-2; __jda=122270672.1743494489941124857168.1743494489.1743494489.1743500930.2; b_dh=1271; b_dpr=1; b_webp=1; 3AB9D23F7A4B3CSS=jdd036EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPMAAAAMV6DAPDWIAAAAAC4AV4EUAP5GUSYX; _gia_d=1; b_dw=1247; mba_sid=17435009308221552413957.2; jcap_dvzw_fp=GMAhe8iH2084tnLleZx9qxA00kP2ft4T4OQFwhmTAfW9FmBh4pfA_huG99ne1PNmPc50poxJuf7kR2wUUPXXzA==; TrackerID=E5r0AQOAccc222-yR-7H0XO7xw5jWPspwh4Ro1YMh3fWuNO3k_pgpQ5mH3JCBV0sR3LYn_qCdfV7vszVAYwswOVLKW_5hOQHiTQELyhsE_k; pt_key=AAJn67amADBCGP5vF2blPQF3lD7hGnqMO7NybmHF2bomYsPe31jKxz_HWHMV5yZ8ks3FDyOHdaU; pt_pin=13418883867_p; pt_st=1_HMGNY4offHkyC0UoOoPrrzHbnTN7HGFdtlv6gsTMpqtoFsIF_Bbg7bJAyJq6B3PusOY4HTmUJTnAjrLY7zuKQmRUcV8N6foYWy4UwiLKHNM9kaSsagY5ZUZkrZaS3EhaElG0dh7M6B676aKmG_9RlWB8_iymEg263M1-AzvPxhdpVAMW5YT8tN_J0rJ2OSIH4Oa_f7Jookcqk0sTzkFGy54YMTDhu6g7ALMi; pt_token=apkp61q5; pwdt_id=13418883867_p; sfstoken=tk01mbbe31c26a8sMysyeDMrM242s2i1XvMRXFmzuQdSjcVmV9kb1+31hLIuhF3gKxrDJdQOun4O7qqAGoJKgWr7H6+w; whwswswws=; __jdb=122270672.4.1743494489941124857168|2.1743500930; __jdv=122270672%7Clianmeng__9__kong%7Ct_1001701389_2011078412_4100072861_3003030792%7Cjingfen%7Cbc18474378e54dc89d83c40414e3d491%7C1743500967035; joyytokem=babel_4HfnzzR9fEiGxYamWbf65PnPj9WDMDFCS1FBTzk5MQ==.c3xlcnpye2h3eHR+ZT8KGn4GDwwxeid4MXNnZ21+bnovczFzNRMAPxoYHg45CHMOAw57PQMjeBA/IBgfGh80dSESOnwXHC4qPyk1dmYHeDcIehw0Li9yPgZ9PDU=.12e367ea; shshshfpb=BApXSjxbJ8_BAbsMeNvQS4RDq_u9zlk48BgEIQ74U9xJ1P40IKdeOykK41H2tDJZJjj5f1g; sdtoken=AAbEsBpEIOVjqTAKCQtvQu17qoQxvfRZYQncUCmNoJKlhfTOeLAeaqPZm1GzRPQuVeSrRWN2n4JNk41Ii5Ut0S4kVcnaSm73iqebzRDDs77NmJeogahrLbD9GlboNuTTAA; __jd_ref_cls=Babel_H5FirstClick; joyya=1743500967.1743500973.36.00wkepb")
			// 执行请求
			resp := makeRequest(client, req, proxyConfig.URL)
			resp.RequestID = requestID

			// 更新统计
			stats.update(resp)

			// 写入日志
			if resp.Error != nil {
				fmt.Fprintf(logFile, "请求 %d 失败 [代理: %s]: %v\n",
					requestID, proxyConfig.URL, resp.Error)
			} else {
				fmt.Fprintf(logFile, "请求 %d 成功 [代理: %s]: 状态=%d, 响应=%s\n",
					requestID, proxyConfig.URL, resp.StatusCode, resp.ResponseBody)
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(startTime)

	// 输出统计信息
	fmt.Printf("\n=== 执行统计 ===\n")
	fmt.Printf("总执行时间: %v\n", duration)
	fmt.Printf("成功请求: %d\n", atomic.LoadInt32(&stats.successCount))
	fmt.Printf("失败请求: %d\n", atomic.LoadInt32(&stats.failCount))

	totalRequests := float64(atomic.LoadInt32(&stats.successCount) + atomic.LoadInt32(&stats.failCount))
	if totalRequests > 0 {
		fmt.Printf("平均请求时间: %v\n", time.Duration(stats.totalDuration.Nanoseconds()/int64(totalRequests)))
	}
	fmt.Printf("最小请求时间: %v\n", stats.minDuration)
	fmt.Printf("最大请求时间: %v\n", stats.maxDuration)
	fmt.Printf("实际请求速率: %.2f 请求/秒\n", float64(totalRequests)/duration.Seconds())

	fmt.Printf("\n响应状态码分布:\n")
	for code, count := range stats.statusCodes {
		fmt.Printf("  HTTP %d: %d 请求\n", code, count)
	}

	fmt.Printf("\n错误分布:\n")
	for msg, count := range stats.errorMessages {
		fmt.Printf("  %s: %d 次\n", msg, count)
	}

	fmt.Printf("\n代理使用统计:\n")
	for proxy, count := range stats.proxyStats {
		fmt.Printf("  %s: %d 次\n", proxy, count)
	}
}

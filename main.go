package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
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

type LogItem struct {
	log    string
	random string
}

type ProxySelector struct {
	proxies    []ProxyConfig
	mode       string
	currentIdx uint32
	mu         sync.Mutex
}

// 定义结构体
type Geo struct {
	Lng string `json:"lng"`
	Lat string `json:"lat"`
}

type RequestBody struct {
	ActivityId       string      `json:"activityId"`
	GridInfo         string      `json:"gridInfo"`
	TransParam       string      `json:"transParam"`
	Scene            string      `json:"scene"`
	Args             string      `json:"args"`
	Platform         string      `json:"platform"`
	OrgType          string      `json:"orgType"`
	OpenId           string      `json:"openId"`
	Eid              string      `json:"eid"`
	Fp               string      `json:"fp"`
	Shshshfp         string      `json:"shshshfp"`
	Shshshfpa        string      `json:"shshshfpa"`
	Shshshfpb        string      `json:"shshshfpb"`
	ChildActivityUrl string      `json:"childActivityUrl"`
	UserArea         string      `json:"userArea"`
	Client           string      `json:"client"`
	ClientVersion    string      `json:"clientVersion"`
	Uuid             string      `json:"uuid"`
	JsToken          string      `json:"jsToken"`
	SdkToken         interface{} `json:"sdkToken"`
	PageClick        string      `json:"pageClick"`
	CouponSource     string      `json:"couponSource"`
	Channel          string      `json:"channel"`
	BatchId          string      `json:"batchId"`
	Geo              Geo         `json:"geo"`
	FullUrl          string      `json:"fullUrl"`
	Log              string      `json:"log"`
	Random           string      `json:"random"`
	FloorId          string      `json:"floor_id"`
}
type TransParamData struct {
	BsessionId        string `json:"bsessionId"`
	BabelChannel      string `json:"babelChannel"`
	ActId             string `json:"actId"`
	EnActId           string `json:"enActId"`
	PageId            string `json:"pageId"`
	EncryptCouponFlag string `json:"encryptCouponFlag"`
	RequestChannel    string `json:"requestChannel"`
	JdAtHomePage      string `json:"jdAtHomePage"`
	UtmFlag           string `json:"utmFlag"`
	LocType           string `json:"locType"`
}

var (
	config Config
	stats  Stats
	logs   []map[string]interface{}
)

// ParseLogItems 解析JSON字符串为LogItem数组
func ParseLogItems(jsonStr string) ([]map[string]interface{}, error) {
	var items []map[string]interface{}
	err := json.Unmarshal([]byte(jsonStr), &items)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}
	return items, nil
}

// 使用 map 来存储不确定的参数
type FlexibleRequest struct {
	Body        map[string]interface{} // 存储 body 中的所有参数
	QueryParams map[string]string      // 存储 URL query 中的其他参数
}

// 反序列化：将 URL 编码的字符串转换为 FlexibleRequest
func Deserialize(encodedStr string) (*FlexibleRequest, error) {
	result := &FlexibleRequest{
		Body:        make(map[string]interface{}),
		QueryParams: make(map[string]string),
	}

	// 1. 解析 URL 编码的字符串
	values, err := url.ParseQuery(encodedStr)
	if err != nil {
		return nil, fmt.Errorf("parse query error: %w", err)
	}

	// 2. 处理 body 参数
	bodyStr := values.Get("body")
	if bodyStr != "" {
		// URL 解码
		decodedBody, err := url.QueryUnescape(bodyStr)
		if err != nil {
			return nil, fmt.Errorf("unescape body error: %w", err)
		}

		// JSON 解析
		err = json.Unmarshal([]byte(decodedBody), &result.Body)
		if err != nil {
			return nil, fmt.Errorf("unmarshal json error: %w", err)
		}
	}

	// 3. 处理其他 query 参数
	for key, values := range values {
		if key != "body" && len(values) > 0 {
			result.QueryParams[key] = values[0]
		}
	}

	return result, nil
}

// 序列化：将 FlexibleRequest 转换为 URL 编码的字符串
// func Serialize(req *FlexibleRequest) (string, error) {
// 	params := url.Values{}

// 	// 1. 处理 body 参数
// 	if len(req.Body) > 0 {
// 		bodyBytes, err := json.Marshal(req.Body)
// 		if err != nil {
// 			return "", fmt.Errorf("marshal body error: %w", err)
// 		}
// 		params.Set("body", url.QueryEscape(string(bodyBytes)))
// 	}

// 	// 2. 处理其他 query 参数
// 	for key, value := range req.QueryParams {
// 		params.Set(key, value)
// 	}

// 	return params.Encode(), nil
// }

// 辅助方法：获取嵌套的值
func (r *FlexibleRequest) GetBodyValue(path ...string) interface{} {
	current := interface{}(r.Body)

	for _, key := range path {
		switch v := current.(type) {
		case map[string]interface{}:
			current = v[key]
		default:
			return nil
		}
	}

	return current
}

// 辅助方法：设置嵌套的值
func (r *FlexibleRequest) SetBodyValue(value interface{}, path ...string) {
	if len(path) == 0 {
		return
	}

	current := r.Body
	for i := 0; i < len(path)-1; i++ {
		key := path[i]
		if _, exists := current[key]; !exists {
			current[key] = make(map[string]interface{})
		}
		if next, ok := current[key].(map[string]interface{}); ok {
			current = next
		} else {
			return
		}
	}

	current[path[len(path)-1]] = value
}

// 辅助方法：设置 query 参数
func (r *FlexibleRequest) SetQueryParam(key, value string) {
	if r.QueryParams == nil {
		r.QueryParams = make(map[string]string)
	}
	r.QueryParams[key] = value
}

func initDefaultConfig() {
	config = Config{
		TargetURL:     "https://api.m.jd.com/client.action?functionId=newBabelAwardCollection",
		TotalRequests: 1000,
		ExecuteTime:   "18:34:30",

		PostData: `body=%7B%22activityId%22%3A%224HfnzzR9fEiGxYamWbf65PnPj9WD%22%2C%22gridInfo%22%3A%22%22%2C%22transParam%22%3A%22%7B%5C%22bsessionId%5C%22%3A%5C%226afdb033-8df4-4038-9437-e8478b8ed946%5C%22%2C%5C%22babelChannel%5C%22%3A%5C%22%5C%22%2C%5C%22actId%5C%22%3A%5C%2201845934%5C%22%2C%5C%22enActId%5C%22%3A%5C%224HfnzzR9fEiGxYamWbf65PnPj9WD%5C%22%2C%5C%22pageId%5C%22%3A%5C%225444074%5C%22%2C%5C%22encryptCouponFlag%5C%22%3A%5C%221%5C%22%2C%5C%22requestChannel%5C%22%3A%5C%22h5%5C%22%2C%5C%22jdAtHomePage%5C%22%3A%5C%220%5C%22%2C%5C%22utmFlag%5C%22%3A%5C%220%5C%22%2C%5C%22locType%5C%22%3A%5C%221%5C%22%7D%22%2C%22scene%22%3A%221%22%2C%22args%22%3A%22key%3D05B0E079770AB36FD9A68EB6DD4D0AB515318F78945D297CECC81E8E4D445074BBA9D2EC1EAD82121979DF1C615E7C64_bingo%2CroleId%3DF840E5D973FD2AAA5BC933D0F9B25256E9111BA172589D85B5C79F34D554AC54B97C64250ADE2E4CD5F0A4868DCFB7D805D7F8516397D1971EC9583A16B0F97911F450856106F5AD6EB8273BAEA0C496ECEAA6CE1313D7C85D9AEA3A82A2E62A8D4783BDB164CEA5F5643ACAAA8ED3EE9648D4EBFA93BDF3A724A7FEDD9417F73B54B369F781A9BFCC333D254C2403E4CE9A1E7A95496DDE2E904E4BFDBA845F_bingo%2CstrengthenKey%3D19F512DCD8EE34ABE9C4FB4A92C2F42AE8B0925BF37D2F1D8C149CDA1E70F74C_bingo%22%2C%22platform%22%3A%221%22%2C%22orgType%22%3A%222%22%2C%22openId%22%3A%22-1%22%2C%22pageClickKey%22%3A%22-1%22%2C%22eid%22%3A%226EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPM%22%2C%22fp%22%3A%22558493836a7258e063320b61ead00b02%22%2C%22shshshfp%22%3A%22%22%2C%22shshshfpa%22%3A%22ec986d0e-c4b3-fc55-a223-9798c850ffe6-1743494491%22%2C%22shshshfpb%22%3A%22BApXSbsIB9fBA4aRLf9FnUMUh2TIM-CevAtZlXPWH9xJ1Muam9oG2%22%2C%22childActivityUrl%22%3A%22https%253A%252F%252Fh5static.m.jd.com%252Fmall%252Factive%252F4HfnzzR9fEiGxYamWbf65PnPj9WD%252Findex.html%22%2C%22userArea%22%3A%22-1%22%2C%22client%22%3A%22-1%22%2C%22clientVersion%22%3A%22-1%22%2C%22uuid%22%3A%22-1%22%2C%22osVersion%22%3A%22-1%22%2C%22brand%22%3A%22-1%22%2C%22model%22%3A%22-1%22%2C%22networkType%22%3A%22-1%22%2C%22jda%22%3A%22122270672.1743494489941124857168.1743494489.1743581250.1743589560.6%22%2C%22jsToken%22%3A%22jdd036EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPMAAAAMV6WFIOSIAAAAACQ2T4MCKPKI2QAX%22%2C%22sdkToken%22%3Anull%2C%22pageClick%22%3A%22Babel_Coupon%22%2C%22couponSource%22%3A%22manual%22%2C%22couponSourceDetail%22%3A%22-100%22%2C%22channel%22%3A%22%E9%80%9A%E5%A4%A9%E5%A1%94%E4%BC%9A%E5%9C%BA%22%2C%22batchId%22%3A%221150570894%22%2C%22headArea%22%3A%22%22%2C%22couponTemplateFrom%22%3A%22%22%2C%22mitemAddrId%22%3A%22%22%2C%22geo%22%3A%7B%22lng%22%3A%22%22%2C%22lat%22%3A%22%22%7D%2C%22addressId%22%3A%22%22%2C%22posLng%22%3A%22%22%2C%22posLat%22%3A%22%22%2C%22un_area%22%3A%22%22%2C%22focus%22%3A%22%22%2C%22innerAnchor%22%3A%22%22%2C%22cv%22%3A%222.0%22%2C%22gLng1%22%3A%22%22%2C%22gLat1%22%3A%22%22%2C%22head_area%22%3A%22%22%2C%22receiverLng%22%3A%22%22%2C%22receiverLat%22%3A%22%22%2C%22fullUrl%22%3A%22https%3A%2F%2Fh5static.m.jd.com%2Fmall%2Factive%2F4HfnzzR9fEiGxYamWbf65PnPj9WD%2Findex.html%22%2C%22log%22%3A%221743589567526~1IC7YVBED3tMDFIUnh2azk5MQ%3D%3D.eWVMRV5wa01AWX5kTAguLmBNRVoLPUgdFXl%2BTlpaZGMGRBV5LDo3GxABNAECC2sONCoqITUTJT4DK0I5DCMnA1IyPhNCUwo1PT86f2YwTxMCYyhCWwEZHBMkMTkzQloAYAwyIRIYEhxeLmMfCBU%3D.0ae6c23b~1%2C1~5E22DEC33B024229D26E58A68C30E60D83B17560~1oe4xpa~C~TBFMWRsDbBFcBxR8BB9yeBUNbn4UVRVNFR8aUAoUcgAUfnUVA2l9GFgVQxFlGBtNWV4aDmIbUwEVcQoVfX8UAGB6G1IUQBsVFVcMGXwKG3l0GA1jcB9ZGE0bGxFcBRR8BB9yeBUNbX0UQRVNFW4UFl5LWRECBRUbREAaDhsIAAcLAQEAAgUJBggPAQoJAxsVFURdUBsDFUdMQF9fQ1VeFhUbQFZZFgMbUVVMQExNQlIaGBtJU10aDmIMBx8MBA4VBQsUDBUPGwNlGBtTXRECBRUbVEAaDhsPBAtZUlwPAgUABA5cAQMIBwAJAQYLBFpbAgMJVghbAREUFldJFQkaY1BXBAEaGBtNFQkJAw0KAgsBAQ8OAgEOGBtTXBECFlgbGxFeRFsbDRF6dUlhZEp1BHsAUXF5Vkp0Un1OZWoNZXdJa0wATV9TAAF7UHZxZQ4NcApAfghpAwNxf11ceEpTfw0IfwFMcHNjfVlSAV8IUBEUFldPFQkac1ZWUF9dFHBXVB0aGBtXVkUaDhtaFR8aR1pLFQljBQ0NBx8KAQ8Jah8aRlYbDWgaVRsVFVIaGBtYFR8aVRsVFVIaGBtYFR8aVRtkGxFRW1gbDRFeUl9fUVVMQBsVFVJSFgMbQhEUFlpQFQkaQwoXAh0KFhUbVFVnQhsDFQoBFhUbVVcaDhtLVl1cW1QEeGNeB1Z7DwIaGBtUXRECbwgVDgUUBmQVFVFUW14bDRFZFhUbWkBfFgMbVhFF~0z6on5r%22%2C%22random%22%3A%22OPf3oB81%22%2C%22floor_id%22%3A%22115454591%22%7D&screen=621*1271&client=wh5&clientVersion=1.0.0&sid=&uuid=1743494489941124857168&area=&uemps=&rfs=&xAPIClientLanguage=zh_CN&appid=babelh5&ext=%7B%22sdkToken%22%3Anull%7D&functionId=newBabelAwardCollection&h5st=20250402182608813%3Biawizxp9dqhpwhq2%3B35fa0%3Btk03w71201b1e18na02RmJMEaLgAHAbgjxs0KG3b8OMze-M_JfDAOO-AjX6WPtel835yki5Ayb8-OPCaQzWY4t96diuq%3Baa99a108660ef8cba9af21bd0e0ba7c1%3B5.1%3B1743589567813%3Bt6HsMmbSGRnSGpnV1unQ0J4RNJImOGLm_VImOuMsCWbiOGLmAh4WMusmk_Mm3ebg2Kri2S7WMdbV7ibVIZbi1moV_mriIdri6erVIZLmOGLm_VqTHlYV3lsmOGujMaIVIh4h6m4iJZbW6qLh4ibhIhbi1mbVMl7i3abh7a4iMuMgMiXW41YWLlsmOGuj_uMgMebRMlsmOGujMmLj92ch4xZVCJIVPZrUMuMgMWHmOuMsCm8Zjt3eelbTkdKhilsm0mcT-dITNlHmOuMsCmMi72YUXlsm0mMV_lsmOGujxtsmkmrm0mci9aHWMusmOuMsCKrm0msi9aHWMusmOuMsCObjOGLm8qbRMlsmOusmk_Mm9RaaJdKiXN7XitJmOGLmBxoVApISMusmOuMsCurm0msg5lImOusmOGuj_uMgMSbRMlsmOusmk_sh8uMgMWbRMlsmOusmk_siOGLm5aHWMusmOuMsCurm0msh5lImOusmOGuj9Srm0m8i5lImOusmOGujMaLj92siPZoRF9ImOGLm9aHWMusmOuMsCurm0m8U3lsmOusmk_chOGLm79ImOusmOGuj_uMgM_ImOusmOGuj_uMgMe4RMusmOuMsztMgMeITJdnQJlsmOGujxtsmkmsSPRLh2irg2Obi6ibiMuMgMqrSMusmOuMsztMgMunSMusmk_Mm6WrQOCrh42YUXt8g_2si9usZgt8S3xoVAJ4ZMuMgMqYR7lsmOG_Q%3B82d7fac64610281988eee72889d0ddec&eid=6EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPM&x-api-eid-token=jdd036EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPMAAAAMV6WFIOSIAAAAACQ2T4MCKPKI2QAX`,
		// `body=%7B%22activityId%22%3A%224HfnzzR9fEiGxYamWbf65PnPj9WD%22%2C%22gridInfo%22%3A%22%22%2C%22transParam%22%3A%22%7B%5C%22bsessionId%5C%22%3A%5C%226c72f973-3743-4aff-874d-e2d3d646a840%5C%22%2C%5C%22babelChannel%5C%22%3A%5C%22%5C%22%2C%5C%22actId%5C%22%3A%5C%2201845934%5C%22%2C%5C%22enActId%5C%22%3A%5C%224HfnzzR9fEiGxYamWbf65PnPj9WD%5C%22%2C%5C%22pageId%5C%22%3A%5C%225444074%5C%22%2C%5C%22encryptCouponFlag%5C%22%3A%5C%221%5C%22%2C%5C%22requestChannel%5C%22%3A%5C%22h5%5C%22%2C%5C%22jdAtHomePage%5C%22%3A%5C%220%5C%22%2C%5C%22utmFlag%5C%22%3A%5C%220%5C%22%2C%5C%22locType%5C%22%3A%5C%221%5C%22%7D%22%2C%22scene%22%3A%221%22%2C%22args%22%3A%22key%3D05B0E079770AB36FD9A68EB6DD4D0AB515318F78945D297CECC81E8E4D445074BBA9D2EC1EAD82121979DF1C615E7C64_bingo%2CroleId%3DF840E5D973FD2AAA5BC933D0F9B25256E9111BA172589D85B5C79F34D554AC54B97C64250ADE2E4CD5F0A4868DCFB7D805D7F8516397D1971EC9583A16B0F97911F450856106F5AD6EB8273BAEA0C496ECEAA6CE1313D7C85D9AEA3A82A2E62A8D4783BDB164CEA5F5643ACAAA8ED3EE9648D4EBFA93BDF3A724A7FEDD9417F73B54B369F781A9BFCC333D254C2403E4CE9A1E7A95496DDE2E904E4BFDBA845F_bingo%2CstrengthenKey%3D19F512DCD8EE34ABE9C4FB4A92C2F42AE8B0925BF37D2F1D8C149CDA1E70F74C_bingo%22%2C%22platform%22%3A%221%22%2C%22orgType%22%3A%222%22%2C%22openId%22%3A%22-1%22%2C%22pageClickKey%22%3A%22-1%22%2C%22eid%22%3A%226EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPM%22%2C%22fp%22%3A%22558493836a7258e063320b61ead00b02%22%2C%22shshshfp%22%3A%22bcba984fc4772288df465c4e94c45b7b%22%2C%22shshshfpa%22%3A%22ec986d0e-c4b3-fc55-a223-9798c850ffe6-1743494491%22%2C%22shshshfpb%22%3A%22BApXSOOvJ8_BA9vRb7RtqYPXTe4nPq-VSlanhz4-V9xJ1Muam9oG2%22%2C%22childActivityUrl%22%3A%22https%253A%252F%252Fh5static.m.jd.com%252Fmall%252Factive%252F4HfnzzR9fEiGxYamWbf65PnPj9WD%252Findex.html%253Futm_user%253Dplusmember%2526_ts%253D1742819243430%2526ad_od%253Dshare%2526gxd%253DRnAowmYKPGXfnp4Sq4B_W578vOMp4E7JgUugKDcomXTOIlSPI-BCnvuytD0G7kc%2526gx%253DRnAomTM2PUO_ss8T04FzPCuSv0HqkkASPQ%2526PTAG%253D17053.1.1%2526cu%253Dtrue%2526utm_source%253Dlianmeng__9__kong%2526utm_medium%253Djingfen%2526utm_campaign%253Dt_1001701389_2011078412_4100072861_3003030792%2526utm_term%253Dbc18474378e54dc89d83c40414e3d491%2526preventPV%253D1%2526forceCurrentView%253D1%22%2C%22userArea%22%3A%22-1%22%2C%22client%22%3A%22-1%22%2C%22clientVersion%22%3A%22-1%22%2C%22uuid%22%3A%22-1%22%2C%22osVersion%22%3A%22-1%22%2C%22brand%22%3A%22-1%22%2C%22model%22%3A%22-1%22%2C%22networkType%22%3A%22-1%22%2C%22jda%22%3A%22122270672.1743494489941124857168.1743494489.1743494489.1743500930.2%22%2C%22jsToken%22%3A%22jdd036EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPMAAAAMV6DAPDWIAAAAAC4AV4EUAP5GUSYX%22%2C%22sdkToken%22%3Anull%2C%22pageClick%22%3A%22Babel_Coupon%22%2C%22couponSource%22%3A%22manual%22%2C%22couponSourceDetail%22%3A%22-100%22%2C%22channel%22%3A%22%E9%80%9A%E5%A4%A9%E5%A1%94%E4%BC%9A%E5%9C%BA%22%2C%22batchId%22%3A%221150570894%22%2C%22headArea%22%3A%22%22%2C%22couponTemplateFrom%22%3A%22%22%2C%22mitemAddrId%22%3A%22%22%2C%22geo%22%3A%7B%22lng%22%3A%22%22%2C%22lat%22%3A%22%22%7D%2C%22addressId%22%3A%22%22%2C%22posLng%22%3A%22%22%2C%22posLat%22%3A%22%22%2C%22un_area%22%3A%22%22%2C%22jdv%22%3A%22lianmeng__9__kong%7Ct_1001701389_2011078412_4100072861_3003030792%7Cjingfen%7Cbc18474378e54dc89d83c40414e3d491%22%2C%22focus%22%3A%22%22%2C%22innerAnchor%22%3A%22%22%2C%22cv%22%3A%222.0%22%2C%22gLng1%22%3A%22%22%2C%22gLat1%22%3A%22%22%2C%22head_area%22%3A%22%22%2C%22receiverLng%22%3A%22%22%2C%22receiverLat%22%3A%22%22%2C%22fullUrl%22%3A%22https%3A%2F%2Fh5static.m.jd.com%2Fmall%2Factive%2F4HfnzzR9fEiGxYamWbf65PnPj9WD%2Findex.html%3Futm_user%3Dplusmember%26_ts%3D1742819243430%26ad_od%3Dshare%26gxd%3DRnAowmYKPGXfnp4Sq4B_W578vOMp4E7JgUugKDcomXTOIlSPI-BCnvuytD0G7kc%26gx%3DRnAomTM2PUO_ss8T04FzPCuSv0HqkkASPQ%26PTAG%3D17053.1.1%26cu%3Dtrue%26utm_source%3Dlianmeng__9__kong%26utm_medium%3Djingfen%26utm_campaign%3Dt_1001701389_2011078412_4100072861_3003030792%26utm_term%3Dbc18474378e54dc89d83c40414e3d491%26preventPV%3D1%26forceCurrentView%3D1%22%2C%22log%22%3A%221743500973309~1RM9eyJsuFwMDFCS1FBTzk5MQ%3D%3D.c3xlcnpye2h3eHR%2BZT8KGn4GDwwxeid4MXNnZ21%2BbnovczFzNRMAPxoYHg45CHMOAw57PQMjeBA%2FIBgfGh80dSESOnwXHC4qPyk1dmYHeDcIehw0Li9yPgZ9PDU%3D.12e367ea~1%2C1~1745BA9622A8976B382D634BA7755493E6420A65~1dzrxuv~C~TBFEVBALbBFUCh9kdB9%2Fbh4FZAMcWB5FFW4cG0ZfWhEKYhBVBR5leh5%2BYB8Ea2YdVh9EGx4TUwcdbHEdeGQcDWZhG1IcTRAdFVcBFGdyG3xnFQZkBx9FFUYTah8SXkBfFQkBFRBCRBEKGwMGAwAFCQIIAQQCAAcHBQIJGx4TQFZUGwgTQ0dEX1RFUVUSFRBGUlISAxBXUUdETEZEVhEcG0JVWREKYgcAGwcBDh4DDx8IFQEdB24cG1hbFQkBFRBSRBEKG1NXVgMDWgZUAAIAWAUAAlcHXAFVVgAFXVFVAwcAAVcHFR8SV0ITDRFnUFwCBREcG0YTDQIHDQEEBwMJDgECBwocG1haFQkSWBAdFVVAWxALFXFxSWpiXUtScwpudXJSSn9UeUVhagZjc0JvTAtLW1gEAXBWcnphDgZkDkt6CGIFB3p7XVd%2BTlh7DQN5BUd0c2h7XVkFXwNWFR8SV0QTDRF3Vl1WW1YQcFxSGREcG1xQQREKG1ETGxFDWkATDWgBDQYBGwEFDwJsGxFCVhALbBFRGx4TVhEcG1MTGxFRGx4TVhEcG1MTGxFRG28dFVpfWBALFVVWX1RXUUdEGx4TVlkSAxBEFR8SWlsTDRFHChwEGQESFRBSUWxGGwgTDgoSFRBTUxEKG0BQWVdfVA9TAnt1DktQRBEcG19bFQlrCB4JBB8BZB4TVV9fXhALFVISFRBcRFQSAxBQFU4%3D~0tcz2bx%22%2C%22random%22%3A%22b5HE7yas%22%2C%22floor_id%22%3A%22115454591%22%7D&screen=1247*1271&client=wh5&clientVersion=1.0.0&sid=&uuid=1743494489941124857168&area=&uemps=&rfs=&xAPIClientLanguage=zh_CN&appid=babelh5&ext=%7B%22sdkToken%22%3Anull%7D&functionId=newBabelAwardCollection&h5st=20250401174934694%3Biawizxp9dqhpwhq2%3B35fa0%3Btk03waa8e1cc618n8VXPLmMfV0mnBqF2IjEWv_W_Ear6NUdtDN2VrHPgGu3Tzl_iNimSG7BULtLzHceodV8SdbnnSDoj%3Beef43870beff9bbeec53b43221aa1e8f%3B5.1%3B1743500973694%3Bt6HsMmbSGRnSGpnV1unQ0J4RNJImOGLm_VImOuMsCWbiOGLmAh4WMusmk_Mm3ebg2Kri2S7WMdbV7ibVIZbi1moV_mriIdri6erVIZLmOGLm_VqTHlYV3lsmOGujMaIVIh4h6m4iJZbW6qLh4ibhIhbi1mbVMl7i3abh7a4iMuMgMiXW41YWLlsmOGuj_uMgMebRMlsmOGujMmLj92ch4xZVCJIVPZrUMuMgMWHmOuMsCmcZBVYUglrUYdpdFlsm0mcT-dITNlHmOuMsCmMi72YUXlsm0mMV_lsmOGujxtsmkmrm0mci9aHWMusmOuMsCKrm0msi9aHWMusmOuMsCObjOGLm8qbRMlsmOusmk_MmhZYS3KJieZodstLmOGLmBxoVApISMusmOuMsCurm0msg5lImOusmOGuj_uMgMSbRMlsmOusmk_sh8uMgMWbRMlsmOusmk_siOGLm5aHWMusmOuMsCurm0msh5lImOusmOGuj7Srm0m8i5lImOusmOGujMaLj92siPZoRF9ImOGLm9aHWMusmOuMsCurm0m8U3lsmOusmk_chOGLm79ImOusmOGuj_uMgM_ImOusmOGuj_uMgMe4RMusmOuMsztMgMeITJdnQJlsmOGujxtsmkmsSPRLh2irg2Obi6ibiMuMgMqrSMusmOuMsztMgMunSMusmk_Mm6WrQOCrh42YUXt8g_2si9usZgt8S3xoVAJ4ZMuMgMqYR7lsmOG_Q%3Ba129955a6cf9224220c964d00229d926&eid=6EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPM&x-api-eid-token=jdd036EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPMAAAAMV6DAPDWIAAAAAC4AV4EUAP5GUSYX`,
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

func getLogs() {
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	req, err := http.NewRequest("POST", "http://127.0.0.1:5889/batchLog?count=1000", nil)

	if err != nil {
		fmt.Printf("创建请求失败: %v\n", err)
		return
	}
	// 执行请求
	resp := makeRequest(client, req, "")
	items, err := ParseLogItems(resp.ResponseBody)
	if err != nil {
		fmt.Println("Error:", err)
		return
	}

	logs = items
	// fmt.Printf("长度=%d, 内容=%v\n", len(logs), logs)
}

// func getTransParam(r *string) (*TransParamData, error) {
// 	var transParam TransParamData
// 	err := json.Unmarshal([]byte(r), &transParam)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return &transParam, nil
// }

// 基础版本
func replaceContent(body, newContent string, startMark, endMark string) string {
	bodyBytes := []byte(body)
	startMarkBytes := []byte(startMark)
	endMarkBytes := []byte(endMark)

	startIndex := bytes.Index(bodyBytes, startMarkBytes)
	if startIndex == -1 {
		return body
	}
	startPos := startIndex + len(startMarkBytes)

	endPos := bytes.Index(bodyBytes[startPos:], endMarkBytes)
	if endPos == -1 {
		return body
	}
	endPos += startPos

	// 预分配切片容量
	result := make([]byte, 0, len(bodyBytes)-(endPos-startPos)+len(newContent))
	result = append(result, bodyBytes[:startPos]...)
	result = append(result, newContent...)
	result = append(result, bodyBytes[endPos:]...)

	return string(result)
}

func main() {

	rand.Seed(time.Now().UnixNano())
	initDefaultConfig()

	getLogs()

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

	// fmt.Printf("长度=%d, 内容=%v\n", len(logs), logs[0]["random"])
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	fmt.Printf("\n等待执行中...\n")
	goto START_REQUESTS
	// for {
	// 	select {
	// 	case <-ticker.C:
	// 		remaining := time.Until(targetTime)
	// 		if remaining <= 0 {
	// 			fmt.Printf("\r开始执行!                                          \n")
	// 			goto START_REQUESTS
	// 		}

	// 		hours := int(remaining.Hours())
	// 		minutes := int(remaining.Minutes()) % 60
	// 		seconds := int(remaining.Seconds()) % 60
	// 		milliseconds := int(remaining.Milliseconds()) % 1000

	// 		fmt.Printf("\r距离执行还剩: %02d:%02d:%02d.%03d",
	// 			hours, minutes, seconds, milliseconds)
	// 	}
	// }

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

			result := config.PostData
			if logs[i]["log"].(string) != "" {
				result = replaceContent(config.PostData, logs[i]["log"].(string), "%22log%22%3A%22", "%22")
			}

			if logs[i]["random"].(string) != "" {
				result = replaceContent(result, logs[i]["random"].(string), "%22random%22%3A%22", "%22")
			}

			if err != nil {
				fmt.Printf("Serialize error: %v\n", err)
				return
			}

			// 创建请求
			req, err := http.NewRequest("POST", config.TargetURL,
				strings.NewReader(result))
			if err != nil {
				fmt.Printf("创建请求失败: %v\n", err)
				return
			}

			fmt.Printf("result: %s\n", result)
			// fmt.Printf("NewReader: %s\n", config.PostData)
			// 设置请求头
			for key, value := range config.Headers {
				req.Header.Set(key, value)
			}
			if config.Cookies != "" {
				req.Header.Set("Cookie", config.Cookies)
			}

			req.Header.Set("accept", "*/*")
			req.Header.Set("accept-language", "zh-CN,zh-TW;q=0.9,zh;q=0.8,en-US;q=0.7,en;q=0.6")
			req.Header.Set("cache-control", "no-cache")
			req.Header.Set("content-type", "application/x-www-form-urlencoded")
			req.Header.Set("origin", "https://h5static.m.jd.com")
			req.Header.Set("pragma", "no-cache")
			req.Header.Set("priority", "u=1, i")
			req.Header.Set("referer", "https://h5static.m.jd.com/mall/active/4HfnzzR9fEiGxYamWbf65PnPj9WD/index.html")
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
			req.Header.Set("cookie", "__jdc=122270672; mba_muid=1743494489941124857168; 3AB9D23F7A4B3C9B=6EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPM; b_avif=1; shshshfpa=ec986d0e-c4b3-fc55-a223-9798c850ffe6-1743494491; shshshfpx=ec986d0e-c4b3-fc55-a223-9798c850ffe6-1743494491; exp-ios-universal=1743498091832; qid_fs=1743494554595; qid_uid=674998aa-94fb-4a46-94e1-5dd5297440ef; b_dh=1271; b_dpr=1; b_webp=1; jcap_dvzw_fp=GMAhe8iH2084tnLleZx9qxA00kP2ft4T4OQFwhmTAfW9FmBh4pfA_huG99ne1PNmPc50poxJuf7kR2wUUPXXzA==; whwswswws=; TrackerID=k3z6x41Z2TKEg_3xxpfdv_7ouZ8r4CSARWHq2bjpPgpKY2afNNRwAWmlD8EpLkWzqjVmjQktJNsc-zluhqEi12BYdyZEh_IEjh6Lgl1gKxU; pt_key=AAJn7JW-ADDF3Av_-7ha_kBf_I52TrPMwkuQrA8XXaeTuq0J7VupHVjyG9BFD-s-gBTliV-8PEo; pt_pin=13418883867_p; pt_token=xqs4ne71; pwdt_id=13418883867_p; sfstoken=tk01mbe4a1c62a8sMisyQk5rNWYxF4eArHXN9ZWUhcajb/EzJuG5bHgV5fROER9JPn41qBhS1rWMPwWKzXybe/ZaSsLA; __jdv=122270672%7Clianmeng__9__kong%7Ct_1001701389_2011078412_4100072861_3003030792%7Cjingfen%7Cbc18474378e54dc89d83c40414e3d491%7C1743558099667; b_dw=621; qid_ls=1743500890997; qid_ts=1743579329666; qid_vis=3; qid_evord=6; pt_st=1_QTsNb-lWQMo4BCGF-GBtn0X_RhlAKqOh_Zt65TBnZw1jol0NhsRXRxjfZ4ThODPBU3epmSCOZ-HXTpZNEnrl4VrP4M5QLVoxnU666pl0WNpqiwpjm3R8ZcCyfRDyElgpGFO0v6YMUckZnRcojNO9TNxAwyp27iLWQYI-mCQtRh9ehgHNw-ibdrV8AYR1aHC9PG__arF1GYISOyOeDHcFslxoLWtL74WYKmZi; __jda=122270672.1743494489941124857168.1743494489.1743581250.1743589560.6; __jdb=122270672.1.1743494489941124857168|6.1743589560; mba_sid=17435895609601597934040.1; 3AB9D23F7A4B3CSS=jdd036EKFOKH4IVE3ZJONIWGWQU2QJSZDS4EUJ4VJSPAZEDMQCLEXWXZZKNS7KN72OWK66XXKZEYMZFFG4DNVZB2F47ZOPMAAAAMV6YEVJWYAAAAADJVHQOMKGVNPJYX; _gia_d=1; joyytokem=babel_4HfnzzR9fEiGxYamWbf65PnPj9WDMDFIUnh2azk5MQ==.eWVMRV5wa01AWX5kTAguLmBNRVoLPUgdFXl+TlpaZGMGRBV5LDo3GxABNAECC2sONCoqITUTJT4DK0I5DCMnA1IyPhNCUwo1PT86f2YwTxMCYyhCWwEZHBMkMTkzQloAYAwyIRIYEhxeLmMfCBU=.0ae6c23b; autoOpenApp_downCloseDate_auto=1743589563162_1800000; shshshfpb=BApXSyM0B9fBAbsMeNvQS4RDq_u9zlk48BgEIQ74G9xJ1P40IKdeOykK41H2tDJZJjj5f1g; sdtoken=AAbEsBpEIOVjqTAKCQtvQu17cjpbgg3c8WBAM3zswkr752GgXH1IPuKspI2zuCb5ULx9usr69KImvC3TwmvC63QcPOYYzwitGBDX2T4d7ixL2MOKKWdxw5j157ovW_3vOA; __jd_ref_cls=Babel_H5FirstClick; joyya=1743589561.1743589567.35.0qk1otg")
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

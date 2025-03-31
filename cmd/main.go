package main

import (
	"bufio"
	"flag"
	"os"
	"strings"
	"time"

	"github.com/zqijzqj/mtSecKill/global"
	"github.com/zqijzqj/mtSecKill/logs"
	"github.com/zqijzqj/mtSecKill/secKill"
)

var Time = "19:59:57"
var OpenURL = "https://pro.m.jd.com/mall/active/ftV8Pe6xhLotF4FS6sibs6w8PHU/index.html?_ts=1742817272959&utm_user=plusmember&gx=RnAomTM2H1Kmuvl3z8EqIKFCsE5jAA&gxd=RnAowmMNPWWKmJgU_dJ1W17d7IRZovc&ad_od=share&cu=true&utm_source=lianmeng__10__kong&utm_medium=jingfen&utm_campaign=t_2020918764_3802091_10731340&utm_term=88437a67bdde4d1cae6f07efa7ee4c1f"
var Dom = "#J_babelOptPage > div > div.bab_opt_mod.bab_opt_mod_1-2-0.module_115487653.free_coupon > div > div > div > a"

var skuId = flag.String("sku", "100012043978", "茅台商品ID")
var num = flag.Int("num", 2, "茅台商品ID")
var works = flag.Int("works", 7, "并发数")
var start = flag.String("time", Time, "开始时间---不带日期")
var brwoserPath = flag.String("execPath", "", "浏览器执行路径，路径不能有空格")

var openURL = flag.String("openURL", OpenURL, "开始时间---不带日期")
var dom = flag.String("dom", Dom, "开始时间---不带日期")

func init() {
	flag.Parse()
}

func main() {
	var err error
	execPath := ""
	if *brwoserPath != "" {
		execPath = *brwoserPath
	}
RE:
	jdSecKill := secKill.NewJdSecKill(execPath, *skuId, *num, *works, *openURL, *dom)
	jdSecKill.StartTime, err = global.Hour2Unix(*start)
	if err != nil {
		logs.Fatal("开始时间初始化失败", err)
	}

	if jdSecKill.StartTime.Unix() < time.Now().Unix() {
		jdSecKill.StartTime = jdSecKill.StartTime.AddDate(0, 0, 1)
	}
	jdSecKill.SyncJdTime()
	logs.PrintlnInfo("开始执行时间为：", jdSecKill.StartTime.Format(global.DateTimeFormatStr))

	err = jdSecKill.Run()
	if err != nil {
		if strings.Contains(err.Error(), "exec") {
			logs.PrintlnInfo("默认浏览器执行路径未找到，" + execPath + "  请重新输入：")
			scanner := bufio.NewScanner(os.Stdin)
			for scanner.Scan() {
				execPath = scanner.Text()
				if execPath != "" {
					break
				}
			}
			goto RE
		}
		logs.Fatal(err)
	}
}

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

var Time = "19:59:59"
var skuId = flag.String("sku", "100012043978", "茅台商品ID")
var num = flag.Int("num", 2, "茅台商品ID")
var works = flag.Int("works", 7, "并发数")
var start = flag.String("time", Time, "开始时间---不带日期")
var brwoserPath = flag.String("execPath", "", "浏览器执行路径，路径不能有空格")

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
	jdSecKill := secKill.NewJdSecKill(execPath, *skuId, *num, *works)
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

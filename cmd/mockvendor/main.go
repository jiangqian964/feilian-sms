// mockvendor 是 T13 全链路冒烟使用的本地厂商短信桩。
// 启动示例：
//
//	mockvendor -listen 127.0.0.1:19090 -dump-dir ./tmp/dump
//
// 运行中可 POST /__control 切换 success/business_error/http_error 模式。
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:19090", "监听地址")
	mode := flag.String("mode", modeSuccess, "初始模式：success/business_error/http_error")
	failCode := flag.Int("fail-code", 50001, "business_error 模式回传的厂商业务错误码")
	failMessage := flag.String("fail-message", "模拟业务失败", "business_error 模式回传的错误描述")
	dumpDir := flag.String("dump-dir", "", "请求体落盘目录（req-NNN.json + last.json），留空不落盘")
	flag.Parse()

	if !validModes[*mode] {
		fmt.Fprintf(os.Stderr, "非法初始模式 %q\n", *mode)
		os.Exit(2)
	}

	stub := &vendorStub{
		mode:        *mode,
		failCode:    *failCode,
		failMessage: *failMessage,
		dumpDir:     *dumpDir,
	}

	fmt.Fprintf(os.Stderr, "mockvendor 启动 listen=%s mode=%s dump=%q\n",
		*listen, *mode, *dumpDir)
	if err := http.ListenAndServe(*listen, stub); err != nil {
		fmt.Fprintln(os.Stderr, "mockvendor 退出:", err)
		os.Exit(1)
	}
}

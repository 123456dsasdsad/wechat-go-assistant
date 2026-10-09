package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/personalwechat"
	"os"
	"os/signal"
)

func main() {
	path := flag.String("cookie", "", "absolute private cookie path")
	flag.Parse()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	rows, e := personalwechat.Login(ctx, *path, func(qr string) { fmt.Println("请使用备用微信扫码验证：\n" + qr) })
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	fmt.Println("已登录。请为自己的操作者联系人设置唯一备注，再填写 personal_wechat.friend_remark：")
	for _, v := range rows {
		fmt.Println(v)
	}
}

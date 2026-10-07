package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/weixin"
	qrcode "github.com/skip2/go-qrcode"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Println("纯 Go 微信客户端 0.1.0\n命令: login | listen | reply | send-file | send-image\n每个命令使用 -h 查看参数。程序不依赖 OpenClaw 或 Node.js。")
		return nil
	}
	command := args[0]
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	statePath := fs.String("state", "data/weixin.json", "授权和接收状态文件（包含私密凭据）")
	qrPath := fs.String("qr", "data/login-qr.png", "login：二维码 PNG 路径")
	once := fs.Bool("once", false, "listen：接收一批后退出，可能是空批次")
	testReply := fs.Bool("test-reply", false, "listen：向扫码用户回复固定测试文字，验证往返")
	mediaDir := fs.String("media-dir", "", "listen：保存收到的图片和文件；为空则仅显示元数据")
	text := fs.String("text", "", "reply：回复文字；为空则从标准输入读取")
	filePath := fs.String("file", "", "send-file/send-image：本地文件路径")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if command == "login" {
		release, err := lockState(*statePath)
		if err != nil {
			return err
		}
		defer release()
		return login(ctx, *statePath, *qrPath)
	}
	if command != "listen" && command != "reply" && command != "send-file" && command != "send-image" {
		return errors.New("unknown command; use help")
	}
	var release func()
	if command == "listen" {
		var err error
		release, err = lockState(*statePath)
		if err != nil {
			return err
		}
		defer release()
	}
	state, err := weixin.LoadState(*statePath)
	if err != nil {
		return fmt.Errorf("读取授权失败，请先运行 login: %w", err)
	}
	client, err := weixin.New(weixin.Options{BaseURL: state.Account.BaseURL, Token: state.Account.BotToken})
	if err != nil {
		return err
	}
	if command == "listen" {
		return listen(ctx, client, state, *statePath, *mediaDir, *once, *testReply)
	}
	reply := weixin.Reply{ToUserID: state.Account.OwnerID, ContextToken: state.Contexts[state.Account.OwnerID]}
	if reply.ContextToken == "" {
		return errors.New("尚未接收到你的微信消息，请先运行 listen 并从手机发送消息")
	}
	if command == "reply" {
		if *text == "" {
			data, err := readInput()
			if err != nil {
				return err
			}
			*text = string(data)
		}
		result, err := client.SendText(ctx, reply, *text)
		if err != nil {
			return err
		}
		fmt.Println("接口已接受文字回复，请在手机确认收到。client_id:", result.ClientID)
		return nil
	}
	if *filePath == "" {
		return errors.New("需要 -file 本地文件路径")
	}
	f, err := os.Open(*filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 25*1024*1024+1))
	if err != nil {
		return err
	}
	if len(data) > 25*1024*1024 {
		return errors.New("文件超过本客户端首版的 25 MiB 上限")
	}
	kind := weixin.UploadFile
	if command == "send-image" {
		kind = weixin.UploadImage
	}
	uploaded, err := client.Upload(ctx, reply.ToUserID, kind, data)
	if err != nil {
		return err
	}
	var result weixin.SendResult
	if command == "send-image" {
		result, err = client.SendImage(ctx, reply, uploaded)
	} else {
		result, err = client.SendFile(ctx, reply, filepath.Base(*filePath), uploaded)
	}
	if err != nil {
		return err
	}
	fmt.Println("接口已接受附件，请在手机确认收到。client_id:", result.ClientID)
	return nil
}

func readInput() ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 64*1024+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 64*1024 {
		return nil, errors.New("输入超过 64 KiB")
	}
	return data, nil
}

func lockState(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock := path + ".lock"
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, errors.New("状态文件已被监听或登录进程占用；异常退出后确认进程已停止再移除 .lock 文件")
	}
	if _, err := fmt.Fprintln(f, os.Getpid()); err != nil {
		f.Close()
		os.Remove(lock)
		return nil, err
	}
	if err := f.Close(); err != nil {
		os.Remove(lock)
		return nil, err
	}
	return func() { os.Remove(lock) }, nil
}

func login(ctx context.Context, statePath, qrPath string) error {
	stateAbsolute, err := filepath.Abs(statePath)
	if err != nil {
		return err
	}
	qrAbsolute, err := filepath.Abs(qrPath)
	if err != nil {
		return err
	}
	if strings.EqualFold(stateAbsolute, qrAbsolute) || strings.EqualFold(stateAbsolute+".lock", qrAbsolute) {
		return errors.New("二维码路径不能覆盖授权文件或其锁文件")
	}
	tokens := []string{}
	if previous, err := weixin.LoadState(statePath); err == nil {
		tokens = append(tokens, previous.Account.BotToken)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	client, err := weixin.New(weixin.Options{})
	if err != nil {
		return err
	}
	lastStatus := ""
	account, err := client.Login(ctx, weixin.LoginCallbacks{
		QRCode: func(q weixin.QRCode) error {
			code, err := qrcode.New(q.Content, qrcode.Medium)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(qrPath), 0700); err != nil {
				return err
			}
			png, err := code.PNG(384)
			if err != nil {
				return err
			}
			if err := os.WriteFile(qrPath, png, 0600); err != nil {
				return err
			}
			fmt.Println("请用手机微信扫描下面二维码，并确认授权：")
			fmt.Print(code.ToSmallString(false))
			fmt.Println("二维码图片：", qrPath)
			return nil
		},
		Status: func(status string) {
			if status != lastStatus {
				fmt.Println("登录状态：", status)
				lastStatus = status
			}
		},
		VerifyCode: readVerification,
	}, tokens)
	if err != nil {
		return err
	}
	if err := weixin.SaveState(statePath, weixin.NewState(account)); err != nil {
		return err
	}
	// The QR is a temporary authorization artifact; remove it once no longer needed.
	os.Remove(qrPath)
	fmt.Println("授权已保存。运行 listen -test-reply，再在手机机器人会话发送测试消息。")
	return nil
}

func readVerification(ctx context.Context) (string, error) {
	fmt.Print("请输入手机微信显示的数字：")
	type result struct {
		text string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			ch <- result{text: strings.TrimSpace(scanner.Text())}
		} else {
			err := scanner.Err()
			if err == nil {
				err = io.EOF
			}
			ch <- result{err: err}
		}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-ch:
		return r.text, r.err
	}
}

func listen(ctx context.Context, client *weixin.Client, state *weixin.State, path, mediaDir string, once, testReply bool) error {
	fmt.Fprintln(os.Stderr, "监听扫码用户的单聊消息。Ctrl+C 停止。")
	for {
		err := client.Drain(ctx, state, func(ctx context.Context, msg weixin.Message) error {
			view := struct {
				ID    string   `json:"message_id"`
				Texts []string `json:"texts,omitempty"`
				Files []string `json:"files,omitempty"`
				Saved []string `json:"saved,omitempty"`
			}{ID: msg.Key()}
			for _, item := range msg.Items {
				if item.Text != nil {
					view.Texts = append(view.Texts, item.Text.Text)
				}
				if item.File != nil {
					view.Files = append(view.Files, item.File.Name)
				}
				if mediaDir != "" && (item.Type == weixin.FileType || item.Type == weixin.ImageType) {
					data, err := client.Download(ctx, item)
					if err != nil {
						return err
					}
					digest := sha256.Sum256(data)
					if err := os.MkdirAll(mediaDir, 0700); err != nil {
						return err
					}
					file := filepath.Join(mediaDir, hex.EncodeToString(digest[:])+".bin")
					if err := os.WriteFile(file, data, 0600); err != nil {
						return err
					}
					view.Saved = append(view.Saved, file)
				}
			}
			if err := json.NewEncoder(os.Stdout).Encode(view); err != nil {
				return err
			}
			if testReply {
				digest := sha256.Sum256([]byte(msg.Key()))
				_, err := client.SendText(ctx, weixin.Reply{ToUserID: msg.FromUserID, ContextToken: msg.ContextToken, ClientID: "go-test-" + hex.EncodeToString(digest[:16])}, "Go 微信客户端已收到你的消息。当前是连接测试，尚未调用 AI 或校园工具。")
				return err
			}
			return nil
		}, func(state *weixin.State) error { return weixin.SaveState(path, state) })
		if err != nil {
			return err
		}
		if once {
			return nil
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// account-queue runs the shared gateway queue without starting a chat relay.
package main

import (
	"context"
	"flag"
	"log"
	"os/exec"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/accountupload"
)

func main() {
	queue := flag.String("queue", "", "protected account batch directory")
	pool := flag.String("pool", "", "existing gateway pool directory")
	reload := flag.String("reload", "", "existing PowerShell gateway reload script")
	flag.Parse()
	if *queue == "" || *pool == "" || *reload == "" {
		log.Fatal("queue, pool and reload are required")
	}
	store, err := accountupload.Open(*queue)
	if err != nil {
		log.Fatal("account queue unavailable")
	}
	for {
		for _, id := range store.Pending() {
			receipt, err := store.Receipt(id)
			if err != nil {
				continue
			}
			if receipt.Status == "active" {
				_ = store.MarkNotified(id)
				continue
			}
			unlock, locked, err := accountupload.MutationLock(*pool)
			if err != nil || !locked {
				continue
			}
			err = store.Process(id, *pool, func() error {
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
				defer cancel()
				args := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-File", *reload, "-Root", *pool}
				if receipt.Operation == "delete" {
					args = append(args, "-SkipCatalog")
				}
				return exec.CommandContext(ctx, "powershell.exe", args...).Run()
			})
			unlock()
			if err != nil {
				log.Printf("batch %s awaits activation", id)
			} else {
				_ = store.MarkNotified(id)
			}
		}
		time.Sleep(2 * time.Second)
	}
}

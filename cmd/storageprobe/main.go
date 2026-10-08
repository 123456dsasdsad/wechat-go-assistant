// storageprobe performs offline migration checks without sending any messages.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/metadb"
)

func main() {
	defer metadb.CloseAll()
	dir := flag.String("jobs", "", "private legacy job directory")
	mode := flag.String("mode", "preflight", "preflight, export, generate, legacy, sqlite")
	flag.Parse()
	if *dir == "" {
		panic("jobs_directory_required")
	}
	if *mode == "generate" {
		if e := os.MkdirAll(*dir, 0700); e != nil {
			panic(e)
		}
		for i := 0; i < 1000; i++ {
			j := jobs.Job{ID: fmt.Sprintf("%024x", i), Model: "gpt-6.1-sol", Effort: "high", Input: "synthetic", Owner: "synthetic", ReplyContext: "synthetic", Status: "delivered", Created: time.Unix(int64(i), 0), Progress: strings.Repeat("x", 32<<10)}
			raw, _ := json.Marshal(j)
			if e := os.WriteFile(filepath.Join(*dir, j.ID+".json"), raw, 0600); e != nil {
				panic(e)
			}
		}
		fmt.Println(`{"generated":1000}`)
		return
	}
	if *mode == "preflight" || *mode == "export" {
		s, e := jobs.Open(*dir)
		if e != nil {
			panic(e)
		}
		count := 0
		statuses := map[string]int{}
		if e = s.Each(func(j jobs.Job) error {
			count++
			statuses[j.Status]++
			if *mode == "export" {
				raw, e := json.Marshal(j)
				if e != nil {
					return e
				}
				return os.WriteFile(filepath.Join(*dir, j.ID+".json"), raw, 0600)
			}
			return nil
		}); e != nil {
			panic(e)
		}
		json.NewEncoder(os.Stdout).Encode(map[string]any{"migrated": count, "statuses": statuses, "active": len(s.Active())})
		return
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var archive any
	if *mode == "legacy" {
		entries, e := os.ReadDir(*dir)
		if e != nil {
			panic(e)
		}
		records := map[string]jobs.Job{}
		for _, item := range entries {
			if !strings.HasSuffix(item.Name(), ".json") {
				continue
			}
			raw, e := os.ReadFile(filepath.Join(*dir, item.Name()))
			if e != nil {
				panic(e)
			}
			var j jobs.Job
			if e = json.Unmarshal(raw, &j); e != nil {
				panic(e)
			}
			records[j.ID] = j
		}
		archive = records
	} else {
		s, e := jobs.Open(*dir)
		if e != nil {
			panic(e)
		}
		// Warm the normal status lookup before measuring retained heap.
		s.Recent("synthetic", 5)
		archive = s
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	json.NewEncoder(os.Stdout).Encode(map[string]any{"mode": *mode, "retained_heap_bytes": int64(after.HeapAlloc) - int64(before.HeapAlloc), "fixture_tasks": 1000, "progress_bytes_per_task": 32 << 10})
	runtime.KeepAlive(archive)
}

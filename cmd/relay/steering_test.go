package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOnlyExplicitSupplementJoinsActiveTask(t *testing.T) {
	in := inboundFixture(t)
	ctx := context.Background()
	in.handle(ctx, textMessage("first", "run task"))
	task, _ := in.queue.Claim(time.Now())
	in.handle(ctx, textMessage("ordinary", "additional ordinary question"))
	in.handle(ctx, textMessage("steer", "补充：把结果改成 CSV"))
	in.handle(ctx, textMessage("steer", "补充：把结果改成 CSV"))
	j, _ := in.queue.Snapshot(task.ID)
	if len(in.queue.History()) != 2 || len(j.Supplements) != 1 || j.Supplements[0].Input != "把结果改成 CSV" || j.Supplements[0].State != "pending" {
		t.Fatal(j)
	}
	if !strings.Contains(in.client.(*fakeMessages).text, "等待送入") {
		t.Fatal("ack falsely claimed accepted")
	}
}
func TestNoActiveSupplementQueuesAndEmptySupplementDoesNot(t *testing.T) {
	in := inboundFixture(t)
	ctx := context.Background()
	in.handle(ctx, textMessage("empty", "补充："))
	if len(in.queue.History()) != 0 {
		t.Fatal("empty supplement queued")
	}
	in.handle(ctx, textMessage("late", "补充: 继续处理"))
	history := in.queue.History()
	if len(history) != 1 || history[0].Input != "继续处理" || history[0].Status != "queued" {
		t.Fatal(history)
	}
}

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/123456dsasdsad/wechat-go-assistant/internal/assistant"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/conversations"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/jobs"
	"github.com/123456dsasdsad/wechat-go-assistant/internal/models"
)

func runSchedules(ctx context.Context, plans *assistant.Store, queue *jobs.Store, sessions *conversations.Store, client *liveResultSender, owner string) {
	for pause(ctx, 15*time.Second) {
		err := plans.Dispatch(time.Now(), func(p assistant.Plan, source string) (string, error) {
			if p.Owner != owner {
				return "", fmt.Errorf("schedule_owner_mismatch")
			}
			reply, err := client.contextFor(owner)
			if err != nil || reply == "" {
				return "", fmt.Errorf("schedule_context_unavailable")
			}
			j, err := queue.EnqueuePersonalized(source, p.Input, owner, reply, models.Choice{Model: p.Model, Effort: p.Effort}, nil, p.Conversation, p.Memory)
			if err != nil {
				return "", err
			}
			if err = recordJob(sessions, j); err != nil {
				return "", err
			}
			return j.ID, nil
		})
		if err != nil {
			fmt.Println(`{"type":"schedule_dispatch_retry"}`)
		}
	}
}

package main

import "context"

type inboundConversationKey struct{}

// Capture selection once, before downloads or other asynchronous work.
func (in *inbound) currentID(ctx context.Context) string {
	if id, ok := ctx.Value(inboundConversationKey{}).(string); ok {
		return id
	}
	return in.sessions.Current().ID
}

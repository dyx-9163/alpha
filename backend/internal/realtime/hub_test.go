package realtime

import (
	"fmt"
	"testing"
	"time"
)

func TestHubCoalescesOverflowIntoGapForDelayedSubscriber(t *testing.T) {
	hub := NewHub()
	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	for index := 0; index < 128; index++ {
		hub.Publish(Event{
			Type:       "status.server.updated",
			Resource:   "server",
			ResourceID: fmt.Sprintf("server-%03d", index),
			Version:    int64(index + 1),
		})
	}

	gapCount := 0
	quiet := time.NewTimer(50 * time.Millisecond)
	defer quiet.Stop()
	for {
		select {
		case event := <-events:
			if event.Type == "realtime.gap" {
				gapCount++
			}
		case <-quiet.C:
			if gapCount != 1 {
				t.Fatalf("expected one coalesced overflow gap after more than 64 server changes, got %d", gapCount)
			}
			return
		}
	}
}

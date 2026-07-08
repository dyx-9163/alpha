package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"aifar-deployment/backend/internal/realtime"
	"aifar-deployment/backend/internal/store"
)

func (a *API) events(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	writeSSE(w, "aifar-event", realtime.Event{
		Type:      "realtime.connected",
		Resource:  "control-plane",
		Status:    "connected",
		CreatedAt: time.Now().UTC(),
	})
	if flusher != nil {
		flusher.Flush()
	}
	ch, unsubscribe := a.realtime.Subscribe()
	defer unsubscribe()
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-ch:
			if !a.canSendRealtimeEvent(r, event) {
				continue
			}
			writeSSE(w, "aifar-event", event)
			if flusher != nil {
				flusher.Flush()
			}
		case <-heartbeat.C:
			writeSSE(w, "heartbeat", map[string]any{"time": time.Now().UTC()})
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

func (a *API) canSendRealtimeEvent(r *http.Request, event realtime.Event) bool {
	if !strings.HasPrefix(event.Type, "alert.") {
		return true
	}
	alert, ok := realtimeAlertPayload(event)
	return ok && a.canViewAlert(currentUser(r).Role, alert)
}

func realtimeAlertPayload(event realtime.Event) (store.Alert, bool) {
	raw, ok := event.Payload["alert"]
	if !ok || raw == nil {
		return store.Alert{}, false
	}
	switch value := raw.(type) {
	case store.Alert:
		return value, true
	case map[string]any:
		return store.Alert{
			ID:                 stringFromMap(value, "id"),
			RequiredPermission: stringFromMap(value, "requiredPermission"),
		}, true
	default:
		return store.Alert{}, false
	}
}

func stringFromMap(value map[string]any, key string) string {
	if raw, ok := value[key]; ok {
		return strings.TrimSpace(strings.Trim(rawString(raw), `"`))
	}
	return ""
}

func rawString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		data, _ := json.Marshal(v)
		return string(data)
	}
}

func (a *API) collectorRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := a.store.ListCollectorRuns()
	respond(w, map[string]any{"items": runs}, err)
}

func (a *API) statusSnapshots(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.ListStatusSnapshots(r.URL.Query().Get("scope"), r.URL.Query().Get("serverId"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		out = append(out, statusSnapshotResponse(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func statusSnapshotResponse(item store.StatusSnapshot) map[string]any {
	payload := map[string]any{}
	if strings.TrimSpace(item.Payload) != "" {
		_ = json.Unmarshal([]byte(item.Payload), &payload)
	}
	return map[string]any{
		"scope":       item.Scope,
		"resourceId":  item.ResourceID,
		"serverId":    item.ServerID,
		"status":      item.Status,
		"payload":     payload,
		"lastError":   item.LastError,
		"version":     item.Version,
		"collectedAt": item.CollectedAt,
		"updatedAt":   item.UpdatedAt,
	}
}

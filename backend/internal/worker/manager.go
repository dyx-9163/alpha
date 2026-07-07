package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/realtime"
	"aifar-deployment/backend/internal/store"
)

type Logger struct {
	manager *Manager
	taskID  string
	target  string
}

func (l Logger) Info(format string, args ...any) {
	l.manager.AppendTargetLog(l.taskID, l.target, "info", fmt.Sprintf(format, args...))
}

func (l Logger) Error(format string, args ...any) {
	l.manager.AppendTargetLog(l.taskID, l.target, "error", fmt.Sprintf(format, args...))
}

func (l Logger) TaskID() string {
	return l.taskID
}

func (l Logger) Target(target string) Logger {
	l.target = target
	return l
}

func (l Logger) PlanTarget(target string) {
	_ = l.manager.store.UpsertTaskTarget(l.taskID, target, "pending", "")
}

func (l Logger) StartTarget(target string) {
	_ = l.manager.store.UpsertTaskTarget(l.taskID, target, "running", "")
}

func (l Logger) FinishTarget(target, status, errText string) {
	_ = l.manager.store.UpsertTaskTarget(l.taskID, target, status, errText)
}

func (l Logger) PlanStep(target, name, title string, order int) {
	_ = l.manager.store.UpsertTaskStep(l.taskID, target, name, title, order, "pending", "")
}

func (l Logger) StartStep(target, name, title string, order int) {
	_ = l.manager.store.UpsertTaskStep(l.taskID, target, name, title, order, "running", "")
}

func (l Logger) FinishStep(target, name, status, errText string) {
	_ = l.manager.store.UpsertTaskStep(l.taskID, target, name, "", 0, status, errText)
}

type Job func(ctx context.Context, log Logger) error

type EventPublisher interface {
	Publish(realtime.Event)
}

type Manager struct {
	store              *store.Store
	defaultConcurrency int
	events             EventPublisher
	mu                 sync.Mutex
	active             int
	subscribers        map[string]map[chan store.TaskLog]struct{}
	cancels            map[string]context.CancelFunc
	languages          map[string]string
}

func NewManager(s *store.Store) *Manager {
	return NewManagerWithConcurrency(s, 2)
}

func NewManagerWithConcurrency(s *store.Store, defaultConcurrency int) *Manager {
	if defaultConcurrency < 1 {
		defaultConcurrency = 1
	}
	return &Manager{
		store:              s,
		defaultConcurrency: defaultConcurrency,
		subscribers:        map[string]map[chan store.TaskLog]struct{}{},
		cancels:            map[string]context.CancelFunc{},
		languages:          map[string]string{},
	}
}

func (m *Manager) SetEventPublisher(events EventPublisher) {
	m.mu.Lock()
	m.events = events
	m.mu.Unlock()
}

func (m *Manager) Start(taskType, target, actor string, job Job) (store.Task, error) {
	return m.StartWithLanguage(taskType, target, actor, "", job)
}

func (m *Manager) StartWithLanguage(taskType, target, actor, lang string, job Job) (store.Task, error) {
	task, err := m.store.CreateTask(store.Task{Type: taskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		return task, err
	}
	return m.StartExistingWithLanguage(task, lang, job)
}

func (m *Manager) StartExistingWithLanguage(task store.Task, lang string, job Job) (store.Task, error) {
	if task.ID == "" {
		return task, errors.New("task id is required")
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.cancels[task.ID] = cancel
	m.languages[task.ID] = lang
	m.mu.Unlock()
	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.cancels, task.ID)
			delete(m.languages, task.ID)
			m.mu.Unlock()
		}()
		if !m.acquireSlot(ctx) {
			_ = m.store.UpdateTaskStatus(task.ID, "cancelled", ctx.Err().Error())
			m.publishTaskEvent(task.ID, "cancelled")
			return
		}
		defer m.releaseSlot()
		_ = m.store.UpdateTaskStatus(task.ID, "running", "")
		m.publishTaskEvent(task.ID, "running")
		m.AppendLog(task.ID, "info", i18n.Text(lang, "worker.taskStarted"))
		if err := job(ctx, Logger{manager: m, taskID: task.ID}); err != nil {
			status := "failed"
			if ctx.Err() != nil {
				status = "cancelled"
			}
			m.AppendLog(task.ID, "error", err.Error())
			_ = m.store.UpdateTaskStatus(task.ID, status, err.Error())
			m.publishTaskEvent(task.ID, status)
			return
		}
		m.AppendLog(task.ID, "info", i18n.Text(lang, "worker.taskCompleted"))
		_ = m.store.UpdateTaskStatus(task.ID, "success", "")
		m.publishTaskEvent(task.ID, "success")
	}()
	return task, nil
}

func (m *Manager) acquireSlot(ctx context.Context) bool {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		limit := m.store.DeploymentConcurrency(m.defaultConcurrency)
		m.mu.Lock()
		if m.active < limit {
			m.active++
			m.mu.Unlock()
			return true
		}
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
		}
	}
}

func (m *Manager) releaseSlot() {
	m.mu.Lock()
	if m.active > 0 {
		m.active--
	}
	m.mu.Unlock()
}

func (m *Manager) AppendLog(taskID, level, message string) {
	m.AppendTargetLog(taskID, "", level, message)
}

func (m *Manager) AppendTargetLog(taskID, target, level, message string) {
	entry, err := m.store.AddTaskTargetLog(taskID, target, level, message)
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch := range m.subscribers[taskID] {
		select {
		case ch <- entry:
		default:
		}
	}
}

func (m *Manager) Subscribe(taskID string) (<-chan store.TaskLog, func()) {
	ch := make(chan store.TaskLog, 32)
	m.mu.Lock()
	if m.subscribers[taskID] == nil {
		m.subscribers[taskID] = map[chan store.TaskLog]struct{}{}
	}
	m.subscribers[taskID][ch] = struct{}{}
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		delete(m.subscribers[taskID], ch)
		if len(m.subscribers[taskID]) == 0 {
			delete(m.subscribers, taskID)
		}
		m.mu.Unlock()
		close(ch)
	}
}

func (m *Manager) Cancel(taskID string) bool {
	m.mu.Lock()
	cancel := m.cancels[taskID]
	lang := m.languages[taskID]
	m.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	m.AppendLog(taskID, "warn", i18n.Text(lang, "worker.cancelRequested"))
	return true
}

func (m *Manager) publishTaskEvent(taskID, status string) {
	m.mu.Lock()
	events := m.events
	m.mu.Unlock()
	if events == nil {
		return
	}
	events.Publish(realtime.Event{
		Type:     "task.updated",
		Resource: "task",
		TaskID:   taskID,
		Status:   status,
		Payload:  map[string]any{"taskId": taskID, "status": status},
	})
	if status == "success" || status == "failed" || status == "cancelled" || status == "timeout" {
		events.Publish(realtime.Event{
			Type:     "task.finished",
			Resource: "task",
			TaskID:   taskID,
			Status:   status,
			Payload:  map[string]any{"taskId": taskID, "status": status},
		})
	}
}

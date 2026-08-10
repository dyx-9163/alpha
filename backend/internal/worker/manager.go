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

func (l Logger) TryEnterCommit() bool {
	if !l.manager.tryEnterCommit(l.taskID) {
		return false
	}
	l.manager.mu.Lock()
	lang := l.manager.languages[l.taskID]
	l.manager.mu.Unlock()
	l.manager.AppendTargetLog(l.taskID, l.target, "info", i18n.Text(lang, "worker.taskCommitStarted"))
	return true
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

type TaskLifecycle struct {
	Start  func(context.Context, context.CancelFunc)
	Finish func() error
}

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
	cancelRequested    map[string]bool
	committing         map[string]bool
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
		cancelRequested:    map[string]bool{},
		committing:         map[string]bool{},
		languages:          map[string]string{},
	}
}

func (m *Manager) SetEventPublisher(events EventPublisher) {
	m.mu.Lock()
	m.events = events
	m.mu.Unlock()
}

func (m *Manager) RecoverInterruptedTasks(lang string) ([]store.Task, error) {
	tasks, err := m.store.RecoverInterruptedTasks(i18n.Text(lang, "worker.taskInterruptedByRestart"))
	if err != nil {
		return nil, err
	}
	for _, task := range tasks {
		m.publishTaskEvent(task.ID, "failed")
	}
	return tasks, nil
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
	return m.StartExistingWithLanguageAndLifecycle(task, lang, job, TaskLifecycle{})
}

func (m *Manager) StartExistingWithLanguageAndLifecycle(task store.Task, lang string, job Job, lifecycle TaskLifecycle) (store.Task, error) {
	if task.ID == "" {
		return task, errors.New("task id is required")
	}
	m.mu.Lock()
	if _, active := m.languages[task.ID]; active {
		m.mu.Unlock()
		return task, errors.New(i18n.Text(lang, "worker.taskAlreadyStarted"))
	}
	persisted, _, err := m.store.GetTask(task.ID)
	if err != nil {
		m.mu.Unlock()
		return task, err
	}
	if persisted.Status != "pending" {
		m.mu.Unlock()
		return task, errors.New(i18n.Text(lang, "worker.taskAlreadyStarted"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancels[task.ID] = cancel
	m.languages[task.ID] = lang
	m.mu.Unlock()
	go func() {
		defer func() {
			cancel()
			m.mu.Lock()
			delete(m.cancels, task.ID)
			delete(m.cancelRequested, task.ID)
			delete(m.committing, task.ID)
			delete(m.languages, task.ID)
			m.mu.Unlock()
		}()
		defer func() {
			_, _ = m.store.ReleaseOperationLocksByTaskID(task.ID)
		}()
		lockHeartbeatCtx, stopLockHeartbeat := context.WithCancel(ctx)
		defer stopLockHeartbeat()
		go m.heartbeatOperationLocks(lockHeartbeatCtx, task.ID)
		if lifecycle.Start != nil {
			lifecycle.Start(ctx, cancel)
		}
		finishTask := func(status, errText string) {
			if lifecycle.Finish != nil {
				if cleanupErr := lifecycle.Finish(); cleanupErr != nil {
					m.AppendLog(task.ID, "error", cleanupErr.Error())
					if status == "success" {
						status = "failed"
						errText = cleanupErr.Error()
					}
				}
			}
			m.finalizeTask(task.ID, status, errText)
		}
		if !m.acquireSlot(ctx) {
			finishTask("cancelled", ctx.Err().Error())
			return
		}
		defer m.releaseSlot()
		_ = m.store.UpdateTaskStatus(task.ID, "running", "")
		m.publishTaskEvent(task.ID, "running")
		m.AppendLog(task.ID, "info", i18n.Text(lang, "worker.taskStarted"))
		err, panicked := runJob(ctx, Logger{manager: m, taskID: task.ID}, lang, job)
		status := m.claimJobOutcome(task.ID, ctx, err, panicked)
		if status == "success" {
			m.AppendLog(task.ID, "info", i18n.Text(lang, "worker.taskCompleted"))
			finishTask(status, "")
			return
		}
		if err == nil {
			err = ctx.Err()
			if err == nil {
				err = context.Canceled
			}
		}
		m.AppendLog(task.ID, "error", err.Error())
		finishTask(status, err.Error())
	}()
	return task, nil
}

func runJob(ctx context.Context, log Logger, lang string, job Job) (err error, panicked bool) {
	defer func() {
		if recover() != nil {
			panicked = true
			err = errors.New(i18n.Text(lang, "worker.taskPanicked"))
		}
	}()
	err = job(ctx, log)
	return err, false
}

func (m *Manager) claimJobOutcome(taskID string, ctx context.Context, jobErr error, panicked bool) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cancelRequested := m.cancelRequested[taskID]
	committing := m.committing[taskID]
	switch {
	case panicked:
		delete(m.cancels, taskID)
		delete(m.cancelRequested, taskID)
		delete(m.committing, taskID)
		return "failed"
	case committing && jobErr != nil:
		delete(m.cancelRequested, taskID)
		delete(m.committing, taskID)
		return "failed"
	case committing:
		delete(m.cancelRequested, taskID)
		delete(m.committing, taskID)
		return "success"
	case cancelRequested || ctx.Err() != nil:
		return "cancelled"
	case jobErr != nil:
		delete(m.cancels, taskID)
		delete(m.cancelRequested, taskID)
		delete(m.committing, taskID)
		return "failed"
	default:
		delete(m.cancels, taskID)
		delete(m.cancelRequested, taskID)
		delete(m.committing, taskID)
		return "success"
	}
}

func (m *Manager) finalizeTask(taskID, status, errText string) {
	if err := m.store.UpdateTaskStatus(taskID, status, errText); err != nil {
		return
	}
	m.publishTaskEvent(taskID, status)
}

func (m *Manager) heartbeatOperationLocks(ctx context.Context, taskID string) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		_, _ = m.store.HeartbeatOperationLocksByTaskID(taskID, time.Hour)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Manager) acquireSlot(ctx context.Context) bool {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		limit := m.store.DeploymentConcurrency(m.defaultConcurrency)
		m.mu.Lock()
		if ctx.Err() != nil {
			m.mu.Unlock()
			return false
		}
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
	if cancel != nil {
		m.cancelRequested[taskID] = true
		cancel()
	}
	m.mu.Unlock()
	if cancel == nil {
		return false
	}
	m.AppendLog(taskID, "warn", i18n.Text(lang, "worker.cancelRequested"))
	return true
}

func (m *Manager) tryEnterCommit(taskID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancelRequested[taskID] || m.committing[taskID] || m.cancels[taskID] == nil {
		return false
	}
	delete(m.cancels, taskID)
	m.committing[taskID] = true
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

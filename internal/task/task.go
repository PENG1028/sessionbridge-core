package task

import (
	"sync"
	"time"
)

// Status represents the lifecycle status of a task.
type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// TaskType categorizes the kind of operation the task performs.
type TaskType string

const (
	TypeInstall    TaskType = "install"
	TypeUninstall  TaskType = "uninstall"
	TypeCheck      TaskType = "check"
	TypeCacheClear TaskType = "cache_clear"
)

// Step represents a single sub-step within a task.
type Step struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Task tracks the progress and result of a long-running operation.
type Task struct {
	ID           string   `json:"taskId"`
	Type         TaskType `json:"type"`
	PluginID     string   `json:"pluginId"`
	TargetNodeID string   `json:"targetNodeId"`
	Status       Status   `json:"status"`
	Steps        []Step   `json:"steps"`
	CurrentStep  int      `json:"currentStep"`
	StartedAt    int64    `json:"startedAt"`
	FinishedAt   int64    `json:"finishedAt,omitempty"`
	Error        string   `json:"error,omitempty"`
	Events       []Event  `json:"events,omitempty"`
}

// Event is a timestamped log entry attached to a task.
type Event struct {
	Time      int64  `json:"time"`
	StepIndex int    `json:"stepIndex"`
	Message   string `json:"message"`
	Level     string `json:"level"` // "info", "warn", "error"
}

// Store is a thread-safe in-memory registry of tasks.
type Store struct {
	mu    sync.RWMutex
	tasks map[string]*Task
}

// NewStore creates an empty task Store.
func NewStore() *Store {
	return &Store{tasks: make(map[string]*Task)}
}

// Create inserts a new task into the store.
func (s *Store) Create(task *Task) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tasks[task.ID] = task
}

// Get retrieves a task by ID.
func (s *Store) Get(id string) (*Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	return t, ok
}

// List returns all tasks in the store.
func (s *Store) List() []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*Task, 0, len(s.tasks))
	for _, t := range s.tasks {
		result = append(result, t)
	}
	return result
}

// UpdateStatus changes a task's status and optionally sets an error.
// Terminal statuses (succeeded / failed / cancelled) also set FinishedAt.
func (s *Store) UpdateStatus(id string, status Status, err string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		t.Status = status
		if err != "" {
			t.Error = err
		}
		if status == StatusSucceeded || status == StatusFailed || status == StatusCancelled {
			t.FinishedAt = time.Now().UnixMilli()
		}
	}
}

// AddEvent appends a timestamped event to the task with the given ID.
func (s *Store) AddEvent(id string, stepIndex int, message, level string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tasks[id]; ok {
		t.Events = append(t.Events, Event{
			Time:      time.Now().UnixMilli(),
			StepIndex: stepIndex,
			Message:   message,
			Level:     level,
		})
	}
}

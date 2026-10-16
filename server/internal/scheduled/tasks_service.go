package scheduled

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"mindfs/server/internal/api/usecase"

	"github.com/robfig/cron/v3"
)

func (s *Store) List() ([]Task, error) {
	data, err := s.root.ReadMetaFile(tasksMetaFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Task{}, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return []Task{}, nil
	}
	var tasks []Task
	if err := json.Unmarshal(data, &tasks); err != nil {
		return nil, err
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		return tasks[i].CreatedAt.Before(tasks[j].CreatedAt)
	})
	return tasks, nil
}

type Service struct {
	registry    usecase.Registry
	usecase     *usecase.Service
	broadcaster SessionActivityBroadcaster
	parser      cron.Parser
	cron        *cron.Cron

	mu      sync.Mutex
	entries map[string][]cron.EntryID
	running map[string]bool
}

func NewService(registry usecase.Registry, broadcaster SessionActivityBroadcaster) *Service {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	return &Service{
		registry:    registry,
		usecase:     &usecase.Service{Registry: registry},
		broadcaster: broadcaster,
		parser:      parser,
		cron:        cron.New(cron.WithParser(parser)),
		entries:     map[string][]cron.EntryID{},
		running:     map[string]bool{},
	}
}

func (s *Service) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.cron.Start()
	for _, root := range s.registry.ListRoots() {
		if err := s.ReloadRoot(root.ID); err != nil {
			log.Printf("[scheduled-agent] reload.error root=%s err=%v", root.ID, err)
		}
	}
	go func() {
		<-ctx.Done()
		stopCtx := s.cron.Stop()
		<-stopCtx.Done()
	}()
}

type SaveInput struct {
	ID             string `json:"id"`
	RootID         string `json:"root_id"`
	Name           string `json:"name"`
	Enabled        bool   `json:"enabled"`
	TaskCron       string `json:"task_cron"`
	Agent          string `json:"agent"`
	Model          string `json:"model"`
	Mode           string `json:"mode"`
	Effort         string `json:"effort"`
	FastService    string `json:"fast_service"`
	Prompt         string `json:"prompt"`
	NewSessionCron string `json:"new_session_cron"`
}

func (s *Service) Create(ctx context.Context, in SaveInput) (Task, error) {
	if err := s.validateInput(in); err != nil {
		return Task{}, err
	}
	store, err := s.store(in.RootID)
	if err != nil {
		return Task{}, err
	}
	tasks, err := store.List()
	if err != nil {
		return Task{}, err
	}
	now := time.Now().UTC()
	task := Task{
		ID:             newID(),
		RootID:         strings.TrimSpace(in.RootID),
		Name:           strings.TrimSpace(in.Name),
		Enabled:        in.Enabled,
		TaskCron:       strings.TrimSpace(in.TaskCron),
		Agent:          strings.TrimSpace(in.Agent),
		Model:          strings.TrimSpace(in.Model),
		Mode:           strings.TrimSpace(in.Mode),
		Effort:         strings.TrimSpace(in.Effort),
		FastService:    strings.TrimSpace(in.FastService),
		Prompt:         strings.TrimSpace(in.Prompt),
		NewSessionCron: strings.TrimSpace(in.NewSessionCron),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	tasks = append(tasks, task)
	if err := store.Save(tasks); err != nil {
		return Task{}, err
	}
	if err := s.ReloadRoot(task.RootID); err != nil {
		return Task{}, err
	}
	s.decorateTask(&task)
	return task, nil
}

func (s *Service) Update(ctx context.Context, in SaveInput) (Task, error) {
	if strings.TrimSpace(in.ID) == "" {
		return Task{}, errors.New("task id required")
	}
	if err := s.validateInput(in); err != nil {
		return Task{}, err
	}
	store, err := s.store(in.RootID)
	if err != nil {
		return Task{}, err
	}
	tasks, err := store.List()
	if err != nil {
		return Task{}, err
	}
	for i := range tasks {
		if tasks[i].ID != strings.TrimSpace(in.ID) {
			continue
		}
		tasks[i].Name = strings.TrimSpace(in.Name)
		tasks[i].Enabled = in.Enabled
		tasks[i].TaskCron = strings.TrimSpace(in.TaskCron)
		tasks[i].Agent = strings.TrimSpace(in.Agent)
		tasks[i].Model = strings.TrimSpace(in.Model)
		tasks[i].Mode = strings.TrimSpace(in.Mode)
		tasks[i].Effort = strings.TrimSpace(in.Effort)
		tasks[i].FastService = strings.TrimSpace(in.FastService)
		tasks[i].Prompt = strings.TrimSpace(in.Prompt)
		tasks[i].NewSessionCron = strings.TrimSpace(in.NewSessionCron)
		tasks[i].UpdatedAt = time.Now().UTC()
		if err := store.Save(tasks); err != nil {
			return Task{}, err
		}
		if err := s.ReloadRoot(in.RootID); err != nil {
			return Task{}, err
		}
		task := tasks[i]
		s.decorateTask(&task)
		return task, nil
	}
	return Task{}, errors.New("task not found")
}

func (s *Service) Delete(ctx context.Context, rootID, id string) error {
	store, err := s.store(rootID)
	if err != nil {
		return err
	}
	tasks, err := store.List()
	if err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	next := tasks[:0]
	found := false
	for _, task := range tasks {
		if task.ID == id {
			found = true
			continue
		}
		next = append(next, task)
	}
	if !found {
		return errors.New("task not found")
	}
	if err := store.Save(next); err != nil {
		return err
	}
	return s.ReloadRoot(rootID)
}

func (s *Service) RunNow(ctx context.Context, rootID, id string) (Task, error) {
	task, err := s.findTask(rootID, id)
	if err != nil {
		return Task{}, err
	}
	if err := s.runTask(ctx, task, true); err != nil {
		task.LastError = err.Error()
	}
	updated, err := s.findTask(rootID, id)
	if err != nil {
		return Task{}, err
	}
	s.decorateTask(&updated)
	return updated, nil
}

func (s *Service) ReloadRoot(rootID string) error {
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return errors.New("root id required")
	}
	tasks, err := s.List(context.Background(), rootID)
	if err != nil {
		return err
	}
	s.mu.Lock()
	for _, id := range s.entries[rootID] {
		s.cron.Remove(id)
	}
	delete(s.entries, rootID)
	s.mu.Unlock()
	for _, task := range tasks {
		task := task
		if !task.Enabled {
			continue
		}
		entryID, err := s.cron.AddFunc(task.TaskCron, func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := s.runTask(ctx, task, false); err != nil {
				log.Printf("[scheduled-agent] run.error root=%s task=%s err=%v", task.RootID, task.ID, err)
			}
		})
		if err != nil {
			return err
		}
		s.mu.Lock()
		s.entries[rootID] = append(s.entries[rootID], entryID)
		s.mu.Unlock()
	}
	return nil
}

func (s *Service) RemoveRoot(rootID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range s.entries[strings.TrimSpace(rootID)] {
		s.cron.Remove(id)
	}
	delete(s.entries, strings.TrimSpace(rootID))
}

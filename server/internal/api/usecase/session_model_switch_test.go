package usecase

import (
	"context"
	"testing"

	"mindfs/server/internal/session"
)

func TestSendMessagePersistsSelectedModelBeforeTurnStarts(t *testing.T) {
	ctx := context.Background()
	manager, key := newEarlyUserPersistManager(t)
	current, err := manager.Get(ctx, key, 0)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if err := manager.UpdateModel(ctx, current, "model-a"); err != nil {
		t.Fatalf("seed model A: %v", err)
	}

	root := manager.Root()
	service := Service{Registry: &commandTestRegistry{root: root, manager: manager}}
	var checkedAtStart bool
	err = service.SendMessage(ctx, SendMessageInput{
		RootID:  root.ID,
		Key:     key,
		Agent:   "claude",
		Model:   "model-b",
		Content: "use model B",
		OnStart: func(start MessageStart) {
			checkedAtStart = true
			if start.Model != "model-b" {
				t.Errorf("OnStart model = %q, want selected model B", start.Model)
			}
			live, err := manager.Get(ctx, key, 0)
			if err != nil {
				t.Errorf("Get during OnStart: %v", err)
				return
			}
			if live.Model != "model-b" {
				t.Errorf("session model during OnStart = %q, want model B", live.Model)
			}
			if len(live.Exchanges) == 0 || live.Exchanges[len(live.Exchanges)-1].Model != "model-b" {
				got := "<missing user exchange>"
				if len(live.Exchanges) > 0 {
					got = live.Exchanges[len(live.Exchanges)-1].Model
				}
				t.Errorf("latest persisted exchange model during OnStart = %q, want model B", got)
			}
		},
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if !checkedAtStart {
		t.Fatal("OnStart was not called")
	}
}

func TestCurrentModelForRuntimeUsesLiveModelBeforePersistedSnapshot(t *testing.T) {
	current := &session.Session{
		Model: "model-b",
		Exchanges: []session.Exchange{
			{Role: "user", Model: "model-b"},
		},
	}
	runtime := &fakeUsecaseAgentSession{id: "existing", model: "model-a"}

	if got := currentModelForRuntime(current, runtime); got != "model-a" {
		t.Fatalf("current model = %q, want live runtime model A", got)
	}
}

package usecase

import (
	"context"
	"testing"
	"time"
)

// Router 回合的 ctx 带的是**模型调用**死线（生产 180s）。回合一旦超时，这个 ctx
// 已经过期；落盘阶段若继续用它，真实原因（回合超时）会被
// "persist turn blocks: context deadline exceeded" 掩掉，助手行还会只剩元数据。
// 落盘必须换一个不受回合死线约束、但有自己上限的 ctx。
func TestTurnPersistenceContextOutlivesTurnDeadline(t *testing.T) {
	turnCtx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	<-turnCtx.Done()

	persistCtx, persistCancel := turnPersistenceContext(turnCtx)
	defer persistCancel()
	if err := persistCtx.Err(); err != nil {
		t.Fatalf("persist ctx inherited the expired turn deadline: %v", err)
	}
	deadline, ok := persistCtx.Deadline()
	if !ok {
		t.Fatal("persist ctx has no bound of its own")
	}
	if remaining := time.Until(deadline); remaining < turnPersistenceTimeout/2 {
		t.Fatalf("persist ctx remaining = %v, want close to its own %v", remaining, turnPersistenceTimeout)
	}
}

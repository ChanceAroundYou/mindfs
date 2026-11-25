package api

import (
	"context"
	"log"
	"time"
)

// 回合空闲看门狗。
//
// ACP 的 `session/prompt` 没有死线，SDK 只在 ctx 被取消时才向 agent 发 cancel。
// agent 一旦卡住（后台任务、网络、内部死锁），`SendMessage` 永不返回，`pending`
// 与前端「运行中」就一直亮着 —— 实测 2026-11-25 `/compact` 那一轮 19:49:07 发出、
// 20:00:08 才有落盘，这 11 分钟里没有任何更新，用户看到的就是「已完成却仍在运行」。
//
// 判据用「连续多久没有任何 OnUpdate」：比单条命令的工具超时（Hermes 420s）宽得多，
// 正常的长工具不会触发；真正的卡死则以小时计。
const (
	sessionTurnIdleTimeout = 30 * time.Minute
	sessionTurnIdlePoll    = 30 * time.Second
)

// watchSessionTurnIdle 在回合期间轮询空闲时长，超阈值就取消回合 ctx。
// done 关闭（正常收尾）或 ctx 结束即退出。
func watchSessionTurnIdle(
	ctx context.Context,
	cancel context.CancelFunc,
	tracker *turnUpdateTracker,
	timeout time.Duration,
	poll time.Duration,
	rootID, key, requestID string,
	done <-chan struct{},
) {
	if timeout <= 0 {
		timeout = sessionTurnIdleTimeout
	}
	if poll <= 0 {
		poll = sessionTurnIdlePoll
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			idle := tracker.IdleFor()
			if idle < timeout {
				continue
			}
			log.Printf("[ws] session.turn.idle_timeout root=%s session=%s request=%s idle=%s timeout=%s —— 回合连续无更新，取消本轮",
				rootID, key, requestID, idle.Round(time.Second), timeout)
			cancel()
			return
		}
	}
}

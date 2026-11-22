package kanban

import (
	"context"
	"log"
	"strings"
)

// 任务卡角标（W3-4）：Router 还没处理完的健康事件。
//
// 语义只有一句话：**这件事 Router 还没看过**。所以判据取事件账本的未处理状态
// （pending / claimed），而不是「曾经发生过什么」—— done / skipped / failed
// 都已经进了任务详情的监管记录，卡片不该再挂一颗擦不掉的红点。
// 标签文案与展示由前端决定（web/src/components/task/routerAlertModel.ts），
// 「什么算未处理」留在这里，浏览器不重算。
//
// 列表走一次批量查询（不做 N+1）；单任务读口也补上，因为详情广播
// （broadcastTask → GetDetail → GetTask）与看板卡片必须是同一种形状，
// 否则 WS 推一次详情就会把角标擦掉。

// TaskRouterAlert 是卡片角标的载荷。Since 是同类未处理事件里最早那条的时间
// （账本里的时间字符串原样透传，前端只格式化）。
type TaskRouterAlert struct {
	Kind  string `json:"kind"`
	Count int    `json:"count"`
	Since string `json:"since"`
}

// attachRouterAlerts 给一批任务补角标，一次查询覆盖整页。
func (s *TaskStore) attachRouterAlerts(ctx context.Context, tasks []Task) {
	if s == nil || len(tasks) == 0 {
		return
	}
	index := make(map[string]int, len(tasks))
	placeholders := make([]string, 0, len(tasks))
	args := make([]any, 0, len(tasks)+4)
	for i := range tasks {
		index[tasks[i].ID] = i
		placeholders = append(placeholders, "?")
		args = append(args, tasks[i].ID)
	}
	args = append(args, string(RouterEventTurnInterrupted), string(RouterEventTurnStalled))
	args = append(args, RouterEventPending, RouterEventClaimed)
	rows, err := s.db.QueryContext(ctx, `
SELECT task_id, kind, COUNT(*), MIN(created_at)
FROM router_events
WHERE task_id IN (`+strings.Join(placeholders, ",")+`)
  AND kind IN (?, ?)
  AND status IN (?, ?)
GROUP BY task_id, kind`, args...)
	if err != nil {
		// 角标是装饰：读不出来就当没有，绝不能让看板列表整体失败。
		log.Printf("[kanban/tasks] router alert query failed err=%v", err)
		return
	}
	defer rows.Close()
	alerts := map[string]*TaskRouterAlert{}
	for rows.Next() {
		var taskID, kind, since string
		var count int
		if err := rows.Scan(&taskID, &kind, &count, &since); err != nil {
			log.Printf("[kanban/tasks] router alert scan failed err=%v", err)
			return
		}
		current := alerts[taskID]
		if current == nil {
			alerts[taskID] = &TaskRouterAlert{Kind: kind, Count: count, Since: since}
			continue
		}
		// 两档同时挂着时中断优先：被重启带走的一轮比「还在慢慢跑」更该被看见。
		// 同一档由 GROUP BY 合成一行，不会走到这里。
		if kind == string(RouterEventTurnInterrupted) && current.Kind != kind {
			alerts[taskID] = &TaskRouterAlert{Kind: kind, Count: count, Since: since}
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("[kanban/tasks] router alert rows failed err=%v", err)
		return
	}
	for taskID, alert := range alerts {
		if i, ok := index[taskID]; ok {
			tasks[i].RouterAlert = alert
		}
	}
}

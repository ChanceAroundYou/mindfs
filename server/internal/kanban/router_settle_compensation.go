package kanban

import (
	"context"
	"log"
	"time"
)

// 结算补偿的窗口与批量。
//
// 窗口取 7 天：补偿是「把丢掉的汇报补上」，补一周内的失败结算有意义，把几个月
// 前的旧账全部翻出来重跑模型只是在烧预算。每拍最多补 5 条，避免一次扫描把这一
// 拍的模型预算吃光；没补完的下一拍接着补（turn_id 的补偿前缀保证不会重复）。
const (
	routerSettleCompensationWindow = 7 * 24 * time.Hour
	routerSettleCompensationLimit  = 5
)

// requeueFailedSettleEvents 补偿「Run 已经成功结算、汇报却没送出去」的事件。
//
// 现场（llmux task_260f5740cced704f，2026-11-20）：run_settled 的汇报回合撞上
// 180s 死线落 failed，失败事件不重放 —— 任务最终成功跑完，用户看到的最后一行
// 还是那条「出错重试」提示，也没有任何总结。回合内的重试（RetryEvent）救不了
// 这种「重试预算已用尽才失败」的事件，所以每拍单独扫一遍。
//
// 只补可重试原因的失败：参数/校验类错误再跑一次只会再错一次，白白多一条噪声。
func (rs *RouterService) requeueFailedSettleEvents(ctx context.Context, now time.Time) {
	if rs == nil || rs.store == nil {
		return
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	events, err := rs.store.ListFailedSettleEvents(ctx, now.Add(-routerSettleCompensationWindow), routerSettleCompensationLimit)
	if err != nil {
		log.Printf("[router/coordinate] settle.requeue.list.error root=%s err=%v", rs.rootID, err)
		return
	}
	for _, event := range events {
		if supervisionFailureKindForText(event.LastError) != SupervisionFailureRetryable {
			continue
		}
		requeued, err := rs.store.RequeueFailedEventForCompensation(ctx, event.ID,
			"requeued for settle compensation: "+event.LastError)
		if err != nil {
			log.Printf("[router/coordinate] settle.requeue.error root=%s event=%s err=%v", rs.rootID, event.ID, err)
			continue
		}
		if requeued {
			log.Printf("[router/coordinate] settle.requeue event=%s task=%s run=%s", event.ID, event.TaskID, event.RunID)
		}
	}
}

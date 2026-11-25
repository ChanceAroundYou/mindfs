package kanban

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"
	"unicode"
)

// 巡检零变化不重复出声（S5）。
//
// 巡检回合（turn_stalled / task_orphaned / turn_interrupted）在「什么都没变」
// 的时候也只能回一句「还在跑，没有新产出」—— 线上 #112 同一句话连刷 16 次，
// 对话流里全是它，真正的结论反而被挤没。判据必须是**机械可判**的：同一任务、
// 同一触发类、同一任务状态、归一化后正文相同，才算「这次说的和上次一样」。
//
// 状态放在进程内登记簿，不落库：重启后第一次巡检重新可见一次（用户至少知道
// 服务还活着），而把它落库会变成又一张需要迁移与清理的表 —— 这条去重只值
// 一个 map。
//
// 被去重的汇报仍然落库（hidden=1），只是不进默认可见流：审计与回看要原文，
// 这一点和 M1 的重复进度汇报同一套通道。

// inspectionDedupeWindow 是「同一句话」重新可见的间隔。用户不希望每 30 秒被
// 念一遍，但也不该永远收不到「它还卡着」的信号。
const inspectionDedupeWindow = 30 * time.Minute

// inspectionDedupeMaxEntries 是登记簿的条数上限。巡检是低频路径，这个上限
// 只是在长时间运行里兜住「任务被删了、条目留着」的泄漏：超过就整批清空，
// 代价是去重状态归零（重新可见一次），远小于无界内存。
const inspectionDedupeMaxEntries = 4096

type inspectionDedupeEntry struct {
	fingerprint string
	at          time.Time
}

// InspectionDedupe 记「这个巡检指纹上一次是什么时候发的」。
//
// key 是任务 ID：同一句话在别的任务上说，对那边的用户是**新信息**，不能因为
// 另一个任务说过就压掉。
type InspectionDedupe struct {
	mu  sync.Mutex
	at  map[string]inspectionDedupeEntry
	now func() time.Time
}

func newInspectionDedupe() *InspectionDedupe {
	return &InspectionDedupe{at: map[string]inspectionDedupeEntry{}, now: time.Now}
}

func (d *InspectionDedupe) nowFn() time.Time {
	if d == nil || d.now == nil {
		return time.Now()
	}
	return d.now()
}

// Repeated 报告这条巡检汇报是不是「上一次已经说过、还没到重新可见窗口」。
// 返回 true 时调用方把它落成 hidden，其余照常。
//
// 先读后写同一把锁：重复的判定与登记必须原子，否则并发的两拍会都判成「第一次」。
func (d *InspectionDedupe) Repeated(taskID, triggerKind, taskStatus, body string) bool {
	if d == nil {
		return false
	}
	fingerprint := inspectionFingerprint(triggerKind, taskStatus, body)
	key := strings.TrimSpace(taskID)
	if key == "" || fingerprint == "" {
		return false
	}
	now := d.nowFn()
	d.mu.Lock()
	defer d.mu.Unlock()
	if prev, ok := d.at[key]; ok {
		if prev.fingerprint == fingerprint && now.Sub(prev.at) < inspectionDedupeWindow {
			return true
		}
	}
	if len(d.at) >= inspectionDedupeMaxEntries {
		d.at = map[string]inspectionDedupeEntry{}
	}
	d.at[key] = inspectionDedupeEntry{fingerprint: fingerprint, at: now}
	return false
}

// inspectionFingerprint 是「同一句话」的判据：触发类 + 任务状态 + 归一化正文。
//
// 归一化按批准口径：去空白、去标点、小写。只差标点或全半角的汇报本来就是同一句
// 话（模型常常把「。」换成「，」重念一遍），不该因为它算新信息把用户再打扰一次。
// 反过来，同一段话在不同触发类下含义不同（「节点停住」和「任务没人管」是两件事），
// 所以触发类进指纹。
func inspectionFingerprint(triggerKind, taskStatus, body string) string {
	normalized := normalizeInspectionText(body)
	if normalized == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(triggerKind) + "\x00" +
		strings.TrimSpace(taskStatus) + "\x00" + normalized))
	return hex.EncodeToString(sum[:])
}

// normalizeInspectionText 去掉空白与标点后转小写，其余字符原样保留。
func normalizeInspectionText(body string) string {
	var b strings.Builder
	for _, r := range body {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// inspectionDedupeApplies 判这个触发类是不是巡检类回合（S5 只管这三类）。
// turn_settled / run_settled 是收尾回合，它们的重复由 M1 的进度抑制与 S4 的
// 「实质变化」判据管，不走这里。
func inspectionDedupeApplies(kind RouterTriggerKind) bool {
	switch kind {
	case RouterTriggerTurnStalled, RouterTriggerTaskOrphaned, RouterTriggerTurnInterrupted:
		return true
	default:
		return false
	}
}

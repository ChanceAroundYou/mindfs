package kanban

import "fmt"

// 一轮决策里 ask_user 与其它动作的组合规则，以及应用前的顺序归一。
//
// 从 router_tools.go 拆出（硬线 500）：那里只保留 dispatch，判定与归一住这里。

// validateSingleTurnAction 校验「必须独占」的动作与同轮其它动作的组合。
//
// 历史上 ask_user / silent 都只能是这一轮唯一的动作，而 router_prompt.md 规则 18
// 又要求「出现待用户拍板的事项就同轮 router_ask_user」—— 两条规则自相矛盾：模型
// 按规则 18 写了 summary + ask_user，被校验整轮 rejected，用户侧零可见（线上 5 条
// 决策全是 `ask_user must be the only action`，task_79ce7b7a / task_7280e0df）。
//
// 产品意图是 say 写结论、ask 写选择题，所以提问允许带**一个** say；silent 仍必须
// 独占，任何「提问 + 执行/监管动作」的组合仍然整轮拒绝 —— 半套动作比没有动作更
// 危险（例如 ask 之后紧跟 run_workflow，用户以为只是在回答问题）。同轮的 silent
// 在 normalizeRouterActionOrder 里就被丢弃（它只是模型的修饰语），这里的 silent
// 独占规则是绕过归一直接调用时的兜底。
func validateSingleTurnAction(actions []RouterAction, kind RouterActionKind) error {
	if len(actions) == 1 {
		return nil
	}
	if kind != RouterActionAskUser {
		return fmt.Errorf("%s must be the only action", kind)
	}
	says, asks := 0, 0
	for _, action := range actions {
		switch action.Kind {
		case RouterActionAskUser:
			asks++
		case RouterActionSay:
			says++
		default:
			return fmt.Errorf("%s can only be combined with one say, not %s", kind, action.Kind)
		}
	}
	if asks != 1 {
		return fmt.Errorf("%s must appear once per turn", kind)
	}
	if says > 1 {
		return fmt.Errorf("%s can only be combined with one say", kind)
	}
	return nil
}

// normalizeRouterActionOrder 把一轮收集到的动作归一到应用顺序：
//
//  1. silent 只在独占一轮时才是决策。模型常把它当「不用另写回复」的修饰语，与
//     message_agent / ask_user / say 同轮调用；旧行为是校验整轮拒绝，提问卡和
//     转交一起丢掉，用户侧零可见，只能等下一轮触发重跑（2026-11-26
//     task_d404e3fb7465e26e：say+silent+ask_user 被拒，router 自查后说
//     「上一轮的提问卡没成功送达」）。丢弃 silent 不损失任何用户可见输出，真正
//     的动作必须留下；多个 silent 也只保留一个。
//  2. say 在前、ask_user 在后，并按新顺序重排 Seq。应用层按 Seq 升序取占位回复
//     （routeApplyState.take）：ask_user 排在前面会先占走同步占位，结论只能另起
//     一条，用户看到「问题在前、解释在后」。
//
// 归一必须放在校验之前 —— validateRouterActions 要求 action.Seq == index，落库、
// 广播与 ClaimAppliedSeq 因此看到同一份顺序。
func normalizeRouterActionOrder(actions []RouterAction) []RouterAction {
	if len(actions) == 0 {
		return actions
	}
	hasOther := false
	for _, action := range actions {
		if action.Kind != RouterActionSilent {
			hasOther = true
			break
		}
	}
	out := make([]RouterAction, 0, len(actions))
	if !hasOther {
		// 全是 silent：保留一个，理由字段随它一起留下。
		out = append(out, actions[0])
	} else {
		for _, action := range actions {
			if action.Kind == RouterActionSilent {
				continue
			}
			out = append(out, action)
		}
	}
	if len(out) >= 2 {
		hasAsk := false
		for _, action := range out {
			if action.Kind == RouterActionAskUser {
				hasAsk = true
				break
			}
		}
		if hasAsk {
			reordered := make([]RouterAction, 0, len(out))
			for _, action := range out {
				if action.Kind != RouterActionAskUser {
					reordered = append(reordered, action)
				}
			}
			for _, action := range out {
				if action.Kind == RouterActionAskUser {
					reordered = append(reordered, action)
				}
			}
			out = reordered
		}
	}
	for index := range out {
		out[index].Seq = index
	}
	return out
}

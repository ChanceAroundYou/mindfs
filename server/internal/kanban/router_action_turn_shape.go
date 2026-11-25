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
// 危险（例如 ask 之后紧跟 run_workflow，用户以为只是在回答问题）。
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

// normalizeRouterActionOrder 把「结论 + 提问」的收集顺序归一到应用顺序：
// say 在前、ask_user 在后，并按新顺序重排 Seq。
//
// 应用层按 Seq 升序取占位回复（routeApplyState.take）：ask_user 排在前面会先占走
// 同步占位，结论只能另起一条，用户看到「问题在前、解释在后」。归一必须放在校验
// 之前 —— validateRouterActions 要求 action.Seq == index，落库、广播与
// ClaimAppliedSeq 因此看到同一份顺序。
func normalizeRouterActionOrder(actions []RouterAction) []RouterAction {
	if len(actions) < 2 {
		return actions
	}
	lead := -1
	for index, action := range actions {
		if action.Kind == RouterActionAskUser {
			lead = index
			break
		}
	}
	if lead < 0 {
		return actions
	}
	out := make([]RouterAction, 0, len(actions))
	for _, action := range actions {
		if action.Kind != RouterActionAskUser {
			out = append(out, action)
		}
	}
	for _, action := range actions {
		if action.Kind == RouterActionAskUser {
			out = append(out, action)
		}
	}
	for index := range out {
		out[index].Seq = index
	}
	return out
}

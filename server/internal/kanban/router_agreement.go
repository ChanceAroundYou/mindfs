package kanban

// routerAgreement 是 shadow 的可解释比较：
//   - legacy 失败/不可判定 -> unknown；
//   - legacy workflow vs run_workflow：同 workflow -> match，否则 mismatch；
//   - legacy direct vs message_agent -> match；
//   - legacy none vs silent -> match；
//   - 其余组合 -> mismatch。
func routerAgreement(legacy *RouterLegacyAction, actions []RouterAction) string {
	if legacy == nil {
		return RouterAgreementNoRouter
	}
	switch legacy.Kind {
	case "failed", "":
		return RouterAgreementUnknown
	case "workflow":
		for _, action := range actions {
			if action.Kind == RouterActionRunWorkflow {
				if action.WorkflowID == legacy.WorkflowID {
					return RouterAgreementMatch
				}
				return RouterAgreementMismatch
			}
		}
		return RouterAgreementMismatch
	case "direct":
		for _, action := range actions {
			if action.Kind == RouterActionMessageAgent {
				return RouterAgreementMatch
			}
		}
		return RouterAgreementMismatch
	case "none":
		if len(actions) == 1 && actions[0].Kind == RouterActionSilent {
			return RouterAgreementMatch
		}
		return RouterAgreementMismatch
	default:
		return RouterAgreementUnknown
	}
}

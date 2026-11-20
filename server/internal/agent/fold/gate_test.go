package fold

import "testing"

func gateTestConfig() Config {
	return Config{RailTokens: 100000, TargetTokens: 20000, MinFoldBytes: 4096, MaxFoldBytes: 262144, ColdRailTokens: 60000}
}

func TestDecideBudgetUnmeasuredCommitsEverything(t *testing.T) {
	budget, decision := DecideBudget(gateTestConfig(), Estimate{}, false, "", 0, false)
	if decision.Decision != GateUnmeasured || !budget.CommitNew {
		t.Fatalf("%+v %+v", budget, decision)
	}
	if budget.MinNewBytes != 0 || budget.MaxNewBytes != 0 {
		t.Fatalf("unmeasured budget carries limits: %+v", budget)
	}
}

func TestDecideBudgetBelowRailCommitsNothing(t *testing.T) {
	budget, decision := DecideBudget(gateTestConfig(), Estimate{Est: 50000, Growth: 1000}, false, "", 50000, false)
	if decision.Decision != GateBelowRail || budget.CommitNew {
		t.Fatalf("%+v %+v", budget, decision)
	}
	if decision.Projected != 51000 {
		t.Fatalf("projected = %d", decision.Projected)
	}
}

func TestDecideBudgetRailFiresWithEventLimits(t *testing.T) {
	budget, decision := DecideBudget(gateTestConfig(), Estimate{Est: 99000, Growth: 2000}, false, "", 99000, false)
	if decision.Decision != GateFold || !budget.CommitNew {
		t.Fatalf("%+v %+v", budget, decision)
	}
	if budget.MinNewBytes != 4096 || budget.MaxNewBytes != 262144 {
		t.Fatalf("event limits = %+v", budget)
	}
}

func TestDecideBudgetLargeBlockFoldsBelowRail(t *testing.T) {
	_, decision := DecideBudget(gateTestConfig(), Estimate{Est: 1000}, false, "", 1000, true)
	if decision.Decision != GateFold {
		t.Fatalf("decision = %s", decision.Decision)
	}
}

func TestDecideBudgetColdRail(t *testing.T) {
	// Cold and above the cold rail: fold even though the rail itself is far.
	if _, decision := DecideBudget(gateTestConfig(), Estimate{Est: 70000}, true, ColdReasonIdle, 70000, false); decision.Decision != GateFold {
		t.Fatalf("cold above cold rail: %s", decision.Decision)
	}
	// Cold but too small for folding to buy anything.
	if _, decision := DecideBudget(gateTestConfig(), Estimate{Est: 30000}, true, ColdReasonIdle, 30000, false); decision.Decision != GateBelowRail {
		t.Fatalf("cold below cold rail: %s", decision.Decision)
	}
}

func TestEstimateScaleClampsBadSamples(t *testing.T) {
	if num, den := (Estimate{Actual: 4000, ForwardEst: 1000}).Scale(); num != 3000 || den != 1000 {
		t.Fatalf("high sample = %d/%d", num, den)
	}
	if num, den := (Estimate{Actual: 1000, ForwardEst: 4000}).Scale(); num != 2000 || den != 4000 {
		t.Fatalf("low sample = %d/%d", num, den)
	}
	if num, den := (Estimate{Actual: 3000, ForwardEst: 1000}).Scale(); num != 3000 || den != 1000 {
		t.Fatalf("in-range sample = %d/%d", num, den)
	}
	if num, den := (Estimate{}).Scale(); num != 1 || den != 1 {
		t.Fatalf("missing sample = %d/%d", num, den)
	}
}

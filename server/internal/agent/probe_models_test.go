package agent

import (
	"testing"

	agenttypes "mindfs/server/internal/agent/types"
)

func TestSanitizeDefaultModelID(t *testing.T) {
	models := []agenttypes.ModelInfo{{ID: "default"}, {ID: "d4p"}, {ID: "d4"}}
	// 过期默认模型（of 不在目录）回退到目录第一个非 default。
	st := SanitizeDefaultModelID(Status{DefaultModelID: "of", Models: models})
	if st.DefaultModelID != "d4p" {
		t.Fatalf("stale default = %q, want d4p", st.DefaultModelID)
	}
	// 目录内模型保留。
	st = SanitizeDefaultModelID(Status{DefaultModelID: "d4p", Models: models})
	if st.DefaultModelID != "d4p" {
		t.Fatalf("valid default = %q, want d4p", st.DefaultModelID)
	}
	// 家族长名（opus）与目录短名（op）等价，保留。
	st = SanitizeDefaultModelID(Status{DefaultModelID: "opus", Models: []agenttypes.ModelInfo{{ID: "op"}, {ID: "d4p"}}})
	if st.DefaultModelID != "opus" {
		t.Fatalf("family default = %q, want opus", st.DefaultModelID)
	}
	// [1m] 后缀不影响归属：os[1m] 与目录短名 os 等价，保留（修复误重置为 opus 的 bug）。
	st = SanitizeDefaultModelID(Status{DefaultModelID: "os[1m]", Models: []agenttypes.ModelInfo{{ID: "os"}, {ID: "d4p"}}})
	if st.DefaultModelID != "os[1m]" {
		t.Fatalf("1m default = %q, want os[1m]", st.DefaultModelID)
	}
	// 无目录（探活未完成）保持现状。
	st = SanitizeDefaultModelID(Status{DefaultModelID: "of"})
	if st.DefaultModelID != "of" {
		t.Fatalf("empty catalog default = %q, want of", st.DefaultModelID)
	}
}

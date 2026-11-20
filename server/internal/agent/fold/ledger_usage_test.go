package fold

import "testing"

// Legacy rows (written before usage_source existed) must keep contributing to
// the total when their rewrite is known, or every existing ledger would read as
// "never measured" after the upgrade.
func TestLedgerSummaryKeepsLegacyKnownSamples(t *testing.T) {
	legacy := ledgerTestEntry(100)
	legacy.RewriteTokens = 40
	missing := ledgerTestEntry(200)
	missing.RewriteTokens = -1
	missing.UsageSource = UsageSourceMissing
	modern := ledgerTestEntry(300)
	modern.RewriteTokens = 60
	modern.UsageSource = UsageSourceResponse

	summary := SummarizeLedger([]LedgerEntry{legacy, missing, modern})
	if summary.TotalRewriteTokens != 100 {
		t.Fatalf("total rewrite = %d, want 100 (legacy + modern)", summary.TotalRewriteTokens)
	}
	if summary.RewriteSamples != 2 || summary.UsageMissing != 1 {
		t.Fatalf("samples=%d missing=%d, want 2/1", summary.RewriteSamples, summary.UsageMissing)
	}
}

func TestLedgerSummarySumsKnownRewrite(t *testing.T) {
	first := ledgerTestEntry(100)
	first.RewriteTokens = 40
	second := ledgerTestEntry(200)
	second.RewriteTokens = 60
	unknown := ledgerTestEntry(300)
	unknown.RewriteTokens = -1
	summary := SummarizeLedger([]LedgerEntry{first, second, unknown})
	if summary.TotalRewriteTokens != 100 {
		t.Fatalf("total rewrite = %d, want 100 (known samples only)", summary.TotalRewriteTokens)
	}
	if summary.Latest.RewriteTokens != -1 {
		t.Fatalf("latest rewrite = %d, want -1", summary.Latest.RewriteTokens)
	}
}

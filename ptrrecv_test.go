package ptrrecv_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis/analysistest"

	ptrrecv "github.com/gomatic/yze-go-ptrrecv"
)

// TestCheckReportsOnlyAPointerNothingRequires names check's claim: a receiver is
// reported only when nothing requires the pointer — no uncopyable machinery, no
// decode contract, a body whose reads survive the rewrite, and a receiver small
// enough to copy. Package a holds a case for each criterion in both directions.
func TestCheckReportsOnlyAPointerNothingRequires(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ptrrecv.Analyzer, "a", "c")
}

// TestAnalyzerAttachesNoSuggestedFix pins the deliberate absence of the rewrite,
// which is a promise of the package doc comment and is invisible to every
// fixture: a diagnostic looks the same with a fix attached and without one.
//
// Deleting the `*` moves the method into the value type's method set, and what
// that costs is decided in packages this analyzer never loads. Measured before
// the fix was withdrawn, each end to end with `go vet` exit 0: a *Money
// MarshalJSON held by value took json.Marshal from {"Total":{"Cents":1234}} to
// {"Total":"12.34"}; a *Temp String took fmt.Println(Temp{21}) from {21} to 21C;
// and applying it across GOROOT made compress/flate die of a stack overflow in
// its own TestMaxStackSize.
func TestAnalyzerAttachesNoSuggestedFix(t *testing.T) {
	results := analysistest.Run(t, analysistest.TestData(), ptrrecv.Analyzer, "a")
	require.NotEmpty(t, results)

	reported := 0
	for _, result := range results {
		for _, diagnostic := range result.Diagnostics {
			reported++
			assert.Empty(t, diagnostic.SuggestedFixes, "no diagnostic may carry a rewrite: %s", diagnostic.Message)
		}
	}
	assert.Positive(t, reported, "the fixture must report, or the assertion above is vacuous")
}

func TestRegistrationIsWellFormed(t *testing.T) {
	assert.NoError(t, ptrrecv.Registration.Validate())
	assert.Equal(t, "yze/ptrrecv", ptrrecv.Registration.RuleID())
	assert.Same(t, ptrrecv.Analyzer, ptrrecv.Registration.Analyzer)
}

// TestAllowFlagExtendsTheDerivedCriterionAndNeverReplacesIt runs both halves of
// the configured exemption under one setting, which is the only way to see the
// claim buildAllow used to make in a comment and nothing proved.
//
// Package b is where it APPLIES: b.special is exempt, and b.Baked — protected
// by the derived criterion rather than by configuration — must stay silent
// beside it, or configuration has replaced what it was meant to extend.
// Package d is where it DOES NOT, written against the matcher: d.special has
// the same NAME and a different package path, so widening the lookup to the
// bare name silences everything there and no coverage number can see it.
func TestAllowFlagExtendsTheDerivedCriterionAndNeverReplacesIt(t *testing.T) {
	require.NoError(t, ptrrecv.Analyzer.Flags.Set("allow", "b.special"))
	t.Cleanup(func() { _ = ptrrecv.Analyzer.Flags.Set("allow", "") })

	analysistest.Run(t, analysistest.TestData(), ptrrecv.Analyzer, "b", "d")
}

// TestAllowFlagRefusesAValueItCannotHonour pins the flag's own refusal at the
// surface the yze framework uses: ApplyConfig calls Flags.Set and turns a
// failure into ErrInvalidSettingValue, which is unreachable for a flag whose
// Set cannot fail.
func TestAllowFlagRefusesAValueItCannotHonour(t *testing.T) {
	err := ptrrecv.Analyzer.Flags.Set("allow", "not-a-type")
	require.Error(t, err)
	assert.ErrorIs(t, err, ptrrecv.ErrInvalidAllowEntry)
}

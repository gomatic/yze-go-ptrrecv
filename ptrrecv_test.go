package ptrrecv_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis/analysistest"

	ptrrecv "github.com/gomatic/yze-go-ptrrecv"
)

// TestCheckReportsOnlyReceiversItCanRewriteAndAlwaysAttachesTheFix names
// check's claim, which the golden files are the evidence for: every diagnostic
// carries the value-receiver rewrite, because being able to rewrite it IS the
// condition for reporting it. A receiver the analyzer cannot rewrite is one the
// author cannot change either, and a diagnostic naming a remedy nobody can take
// is answered with a baseline rather than a fix.
func TestCheckReportsOnlyReceiversItCanRewriteAndAlwaysAttachesTheFix(t *testing.T) {
	analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), ptrrecv.Analyzer, "a", "c")
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

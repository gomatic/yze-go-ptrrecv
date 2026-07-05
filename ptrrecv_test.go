package ptrrecv_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis/analysistest"

	ptrrecv "github.com/gomatic/yze-go-ptrrecv"
)

func TestUnjustifiedPointerReceiversAreReportedAndSafeOnesFixed(t *testing.T) {
	analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), ptrrecv.Analyzer, "a", "c")
}

func TestRegistrationIsWellFormed(t *testing.T) {
	assert.NoError(t, ptrrecv.Registration.Validate())
	assert.Equal(t, "yze/ptrrecv", ptrrecv.Registration.RuleID())
	assert.Same(t, ptrrecv.Analyzer, ptrrecv.Registration.Analyzer)
}

func TestAllowFlagPermitsConfiguredFieldTypes(t *testing.T) {
	require.NoError(t, ptrrecv.Analyzer.Flags.Set("allow", "b.special"))
	t.Cleanup(func() { _ = ptrrecv.Analyzer.Flags.Set("allow", "") })

	analysistest.Run(t, analysistest.TestData(), ptrrecv.Analyzer, "b")
}

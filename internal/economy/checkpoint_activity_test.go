package economy

import (
	"bytes"
	"testing"

	"github.com/nanolathe-gg/nanolathe/internal/sim/checkpoint"
)

func TestCheckpointActivityExtensionLeavesNoHoldBaselineUntouched(t *testing.T) {
	s := &Service{}
	baseline := economyCheckpointBytes(t, s)
	s.Rules = ModernRules{}
	s.EnableActivityReport()
	if !bytes.Equal(baseline, economyCheckpointBytes(t, s)) {
		t.Fatal("rules/report changed baseline bytes")
	}
	s.HoldActive[3][ActivityGeneration] = true
	active := economyCheckpointBytes(t, s)
	if bytes.Equal(baseline, active) {
		t.Fatal("active hold omitted")
	}
	s.HoldActive[4][ActivityGeneration] = true
	if bytes.Equal(active, economyCheckpointBytes(t, s)) {
		t.Fatal("another owner's hold omitted")
	}
	s.HoldActive[4][ActivityGeneration] = false
	var with, without checkpoint.Summary
	if err := s.AppendCheckpointSummary(&with); err != nil {
		t.Fatal(err)
	}
	s.HoldActive[3][ActivityGeneration] = false
	if err := s.AppendCheckpointSummary(&without); err != nil {
		t.Fatal(err)
	}
	if with == without {
		t.Fatal("summary omitted active hold")
	}
	if !bytes.Equal(baseline, economyCheckpointBytes(t, s)) {
		t.Fatal("resumed hold changed baseline")
	}
}

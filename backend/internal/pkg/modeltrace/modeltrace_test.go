package modeltrace

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestPinnedBankMetadata(t *testing.T) {
	metadata, err := BankMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if metadata.SHA256 != "f267d0513aa06981d024ec5394746fceddf44e4e8a75fa6bf8304255f4b32d6d" || metadata.SourceCommit != SourceCommit || len(metadata.Models) != 18 {
		t.Fatal("embedded bank differs from the reviewed snapshot")
	}
}

func TestLongestNumberRunAndAcceptanceBoundary(t *testing.T) {
	for _, test := range []struct {
		text string
		want []int
	}{
		{"requested 300 numbers: 1, 2, 355, 356, 0, 17 explanation 9, 8", []int{1, 2, 355, 17}},
		{"甲 1 2 乙 3 4", []int{1, 2}},
		{"999999999999999999999999999999999 355 0 356", []int{355}},
		{"refused", nil},
	} {
		if got := ParseNumbers(test.text); !reflect.DeepEqual(got, test.want) {
			t.Fatalf("ParseNumbers(%q)=%v, want %v", test.text, got, test.want)
		}
	}
	if MinimumNumbers(300) != 165 || MinimumNumbers(0) != 80 || MinimumNumbers(301) != 166 {
		t.Fatal("incorrect validity threshold")
	}
	if _, err := Analyze([]Output{{Text: strings.Repeat("17,", 164), ExpectedCount: 300}}); err == nil {
		t.Fatal("164 numbers must not pass a 165-number minimum")
	}
	if _, err := Analyze([]Output{{Text: strings.Repeat("17,", 165), ExpectedCount: 300}}); err != nil {
		t.Fatal(err)
	}
}

func TestThreeIndependentChallenges(t *testing.T) {
	challenges, err := Challenges()
	if err != nil {
		t.Fatal(err)
	}
	if len(challenges) != 3 {
		t.Fatal("expected three challenges")
	}
	lengths, ids := map[int]bool{}, map[string]bool{}
	for _, challenge := range challenges {
		if challenge.ExpectedCount < 292 || challenge.ExpectedCount > 332 || lengths[challenge.ExpectedCount] || ids[challenge.ID] {
			t.Fatal("duplicate or out-of-range challenge")
		}
		if !strings.Contains(challenge.Prompt, "禁止调用") || strings.Contains(challenge.Prompt, "HTML") {
			t.Fatal("invalid challenge contract")
		}
		lengths[challenge.ExpectedCount], ids[challenge.ID] = true, true
	}
}

func TestPinnedReferenceAndSampleCountCalibration(t *testing.T) {
	// These public reference answers belong to the bank's training corpus. This
	// verifies the port, not independent accuracy or real-provider behavior.
	data, err := os.ReadFile("testdata/astra-reference.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SourceCommit  string   `json:"source_commit"`
		ExpectedModel string   `json:"expected_model"`
		Outputs       []Output `json:"outputs"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.SourceCommit != SourceCommit {
		t.Fatal("fixture/bank revision mismatch")
	}
	result, err := Analyze(fixture.Outputs)
	if err != nil {
		t.Fatal(err)
	}
	if result.Prediction != fixture.ExpectedModel || result.UsedOutputs != 3 {
		t.Fatalf("unexpected reference attribution: %+v", result)
	}
	var total, familyTotal float64
	for _, candidate := range result.Candidates {
		if math.IsNaN(candidate.Probability) || candidate.Probability < 0 || candidate.Probability > 1 {
			t.Fatal("invalid probability")
		}
		total += candidate.Probability
	}
	for _, family := range result.Families {
		familyTotal += family.Probability
	}
	if math.Abs(total-1) > 1e-12 || math.Abs(familyTotal-1) > 1e-12 {
		t.Fatal("probabilities must sum to one")
	}
	// Three copies of one output must preserve the ranking but use the
	// three-query calibration. Adding a refusal must not change calibration.
	one, err := Analyze(fixture.Outputs[:1])
	if err != nil {
		t.Fatal(err)
	}
	three, err := Analyze([]Output{fixture.Outputs[0], fixture.Outputs[0], fixture.Outputs[0]})
	if err != nil {
		t.Fatal(err)
	}
	withRefusal, err := Analyze(append(append([]Output(nil), fixture.Outputs...), Output{Text: "refused", ExpectedCount: 300}))
	if err != nil {
		t.Fatal(err)
	}
	if one.Prediction != three.Prediction || three.Probability < one.Probability {
		t.Fatal("sample-count calibration changed ranking or reduced confidence for identical samples")
	}
	if !reflect.DeepEqual(result, withRefusal) {
		t.Fatal("invalid answers must not affect attribution")
	}
}

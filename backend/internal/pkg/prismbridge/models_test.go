package prismbridge

import "testing"

func TestNormalizeModel(t *testing.T) {
	if got, err := NormalizeModel("prism-sol"); err != nil || got != "gpt-5.6-sol" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if _, err := NormalizeModel("gpt-4o"); err == nil {
		t.Fatal("unsupported model must fail")
	}
}

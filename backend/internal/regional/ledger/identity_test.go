package ledger

import "testing"

func TestEventJSONIdentityPreservesLargeIntegers(t *testing.T) {
	a := []byte(`{"user_id":9007199254740992,"input_tokens":1}`)
	b := []byte(`{"user_id":9007199254740993,"input_tokens":1}`)
	if sameJSON(a, b) {
		t.Fatal("distinct identities above float precision considered equal")
	}
	if !sameJSON(a, []byte(`{"input_tokens":1,"user_id":9007199254740992}`)) {
		t.Fatal("JSONB key order treated as a conflict")
	}
}

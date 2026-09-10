package hatest

import "testing"

func TestMatchValueRejectsNonNumericOrdering(t *testing.T) {
	if matchValue("unavailable", "17.9", ">=") {
		t.Fatal("unavailable >= 17.9 must fail")
	}
	if matchValue("unknown", 10, ">") {
		t.Fatal("unknown > 10 must fail")
	}
	if !matchValue(20, 10, ">") {
		t.Fatal("20 > 10 must pass")
	}
	if !matchValue("on", "on", "==") {
		t.Fatal("string equality must pass")
	}
	if matchValue("off", "on", "==") {
		t.Fatal("string inequality equality must fail")
	}
}

package models

import (
	"slices"
	"testing"
)

func TestStringArrayValueAndScan(t *testing.T) {
	in := StringArray{"tasks:read", "tasks:write"}
	val, err := in.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	var out StringArray
	if err := out.Scan(val); err != nil {
		t.Fatalf("Scan(%v): %v", val, err)
	}
	if !slices.Equal(in, out) {
		t.Fatalf("round-trip = %v, want %v", out, in)
	}

	empty := StringArray{}
	ev, err := empty.Value()
	if err != nil {
		t.Fatalf("empty Value: %v", err)
	}
	var eout StringArray
	if err := eout.Scan(ev); err != nil {
		t.Fatalf("empty Scan: %v", err)
	}
	if len(eout) != 0 {
		t.Fatalf("empty round-trip = %v, want empty", eout)
	}

	var nilArr StringArray
	nv, err := nilArr.Value()
	if err != nil || nv != nil {
		t.Fatalf("nil Value = (%v, %v), want (nil, nil)", nv, err)
	}
	if err := out.Scan(nil); err != nil || out != nil {
		t.Fatalf("Scan(nil) = (%v, %v), want (nil, nil)", out, err)
	}
}

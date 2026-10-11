package operation

import (
	"errors"
	"testing"
)

func TestNormalizeCanonicalizesAndValidatesParameters(t *testing.T) {
	a, count, err := Normalize(Generate, JSON(`{"prompt":" sky ","height":512,"model":" test ","width":512}`), "PNG")
	if err != nil || count != 1 {
		t.Fatalf("normalize: count=%d, err=%v", count, err)
	}
	b, _, err := Normalize(Generate, JSON(`{"model":"test","width":512,"height":512,"count":1,"prompt":"sky"}`), "PNG")
	if err != nil || string(a) != string(b) {
		t.Fatalf("equivalent parameters differ: %s / %s (%v)", a, b, err)
	}
	for _, raw := range []string{
		`null`, `{}`, `{"model":"test","prompt":"sky","width":512,"height":512,"api_key":"secret"}`,
		`{"model":"test","prompt":"sky","width":512,"height":512,"count":17}`,
	} {
		if _, _, err := Normalize(Generate, JSON(raw), "PNG"); !errors.Is(err, ErrInvalid) {
			t.Errorf("expected invalid parameters for %s, got %v", raw, err)
		}
	}
}

func TestValidateInputRoles(t *testing.T) {
	for _, test := range []struct {
		kind  Kind
		roles []string
		valid bool
	}{
		{Generate, nil, true},
		{Generate, []string{"source"}, false},
		{Upscale, []string{"source"}, true},
		{Upscale, []string{"source", "mask"}, false},
		{Edit, []string{"source", "mask", "reference"}, true},
		{Edit, []string{"mask"}, false},
		{Edit, []string{"source", "mask", "mask"}, false},
	} {
		err := ValidateInputs(test.kind, test.roles)
		if (err == nil) != test.valid {
			t.Errorf("%s %v: %v", test.kind, test.roles, err)
		}
	}
}

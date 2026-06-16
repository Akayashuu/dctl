package dctl

import "testing"

func TestOptIntAndFloat(t *testing.T) {
	// Discord delivers numbers as JSON floats.
	d := InteractionData{Name: "cfg", Options: []InteractionOption{
		{Name: "count", Type: optInteger, Value: float64(5)},
		{Name: "ratio", Type: optNumber, Value: float64(1.5)},
		{Name: "label", Type: optString, Value: "hi"},
	}}

	if n, ok := d.OptInt("count"); !ok || n != 5 {
		t.Errorf("OptInt(count) = %d, %v", n, ok)
	}
	if f, ok := d.OptFloat("ratio"); !ok || f != 1.5 {
		t.Errorf("OptFloat(ratio) = %v, %v", f, ok)
	}
	if _, ok := d.OptInt("label"); ok {
		t.Error("OptInt on a string option must report absent")
	}
	if _, ok := d.OptInt("missing"); ok {
		t.Error("OptInt on a missing option must report absent")
	}
}

func TestOptIntNested(t *testing.T) {
	d := InteractionData{Name: "set", Options: []InteractionOption{{
		Name: "limit", Type: optSubCommand,
		Options: []InteractionOption{{Name: "n", Type: optInteger, Value: float64(42)}},
	}}}
	if n, ok := d.OptInt("n"); !ok || n != 42 {
		t.Errorf("OptInt(n) nested = %d, %v", n, ok)
	}
}

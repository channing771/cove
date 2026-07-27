package eval

import "testing"

func TestExpectStrings(t *testing.T) {
	c := Case{Expect: map[string]any{
		"one":  "hello",
		"many": []any{"a", "b"},
	}}
	if got, ok := ExpectStrings(c, "one"); !ok || len(got) != 1 || got[0] != "hello" {
		t.Fatalf("one = %v %v", got, ok)
	}
	if got, ok := ExpectStrings(c, "many"); !ok || len(got) != 2 {
		t.Fatalf("many = %v %v", got, ok)
	}
	if _, ok := ExpectStrings(c, "absent"); ok {
		t.Fatal("absent should be false")
	}
}

func TestExpectFloatAndInt(t *testing.T) {
	c := Case{Expect: map[string]any{"n": float64(3)}}
	if f, ok := ExpectFloat(c, "n"); !ok || f != 3 {
		t.Fatalf("float = %v %v", f, ok)
	}
	if n, ok := ExpectInt(c, "n"); !ok || n != 3 {
		t.Fatalf("int = %v %v", n, ok)
	}
}

package eval

import (
	"path/filepath"
	"testing"
)

func TestLoadDataset(t *testing.T) {
	ds, err := LoadDataset("testdata/datasets/smoke.json")
	if err != nil {
		t.Fatalf("LoadDataset: %v", err)
	}
	if ds.Name != "smoke" {
		t.Fatalf("name = %q, want smoke", ds.Name)
	}
	if len(ds.Cases) != 1 {
		t.Fatalf("cases = %d, want 1", len(ds.Cases))
	}
	c := ds.Cases[0]
	if c.ID != "greet-basic" {
		t.Fatalf("id = %q", c.ID)
	}
	if got := c.Expect["stop_reason"]; got != "final_answer" {
		t.Fatalf("expect.stop_reason = %v", got)
	}
	if !filepath.IsAbs(c.Cassette) {
		t.Fatalf("cassette not absolute: %q", c.Cassette)
	}
}

func TestLoadDatasetRejectsDuplicateID(t *testing.T) {
	_, err := LoadDataset("testdata/datasets/dup.json")
	if err == nil {
		t.Fatal("want error for duplicate id")
	}
}

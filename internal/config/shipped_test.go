package config

import (
	"testing"

	"antenna/internal/provider"
)

func TestShippedItemsFileParses(t *testing.T) {
	items, err := Load("../../App/Antenna/items.txt")
	if err != nil {
		t.Fatalf("the shipped items.txt does not parse: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	for _, it := range items {
		if it.Kind != provider.ArchiveItem {
			t.Errorf("%s parsed as %v, want an archive item", it.Ref, it.Kind)
		}
	}
	for _, it := range items {
		t.Logf("%s -> %q", it.Ref, it.Title)
	}
}

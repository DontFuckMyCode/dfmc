package codemap

import (
	"testing"
	"time"
)

func TestCloneBuildSample(t *testing.T) {
	now := time.Now()
	sample := BuildSample{
		StartedAt:      now,
		DurationMs:     100,
		FilesRequested: 10,
		FilesProcessed: 9,
		FilesSkipped:   1,
		ParseErrors:    0,
		GraphNodes:     5,
		GraphEdges:     3,
		NodesAdded:     2,
		EdgesAdded:     1,
		Languages:      map[string]int64{"go": 10},
		Directories:    map[string]int64{"/foo": 5},
	}

	clone := cloneBuildSample(sample)

	// Values should be equal
	if clone.DurationMs != sample.DurationMs {
		t.Errorf("DurationMs = %d, want %d", clone.DurationMs, sample.DurationMs)
	}
	if clone.FilesRequested != sample.FilesRequested {
		t.Errorf("FilesRequested = %d, want %d", clone.FilesRequested, sample.FilesRequested)
	}
	if clone.Languages["go"] != 10 {
		t.Errorf("Languages[go] = %d, want 10", clone.Languages["go"])
	}

	// Modification of clone should not affect original
	clone.Languages["go"] = 999
	if sample.Languages["go"] != 10 {
		t.Error("clone modification affected original Languages")
	}
}

func TestCloneBuildSamples(t *testing.T) {
	samples := []BuildSample{
		{DurationMs: 100, Languages: map[string]int64{"go": 1}},
		{DurationMs: 200, Languages: map[string]int64{"ts": 2}},
	}

	clones := cloneBuildSamples(samples)

	if len(clones) != 2 {
		t.Fatalf("len(clones) = %d, want 2", len(clones))
	}
	if clones[0].DurationMs != 100 {
		t.Errorf("clones[0].DurationMs = %d, want 100", clones[0].DurationMs)
	}
	if clones[1].Languages["ts"] != 2 {
		t.Errorf("clones[1].Languages[ts] = %d, want 2", clones[1].Languages["ts"])
	}
}

func TestCloneBuildSamples_empty(t *testing.T) {
	result := cloneBuildSamples(nil)
	if result != nil {
		t.Errorf("cloneBuildSamples(nil) = %v, want nil", result)
	}

	result = cloneBuildSamples([]BuildSample{})
	if result != nil {
		t.Errorf("cloneBuildSamples([]) = %v, want nil", result)
	}
}

func TestCloneCountMap(t *testing.T) {
	input := map[string]int64{"go": 10, "ts": 5, "py": 3}
	clone := cloneCountMap(input)

	if clone["go"] != 10 {
		t.Errorf("clone[go] = %d, want 10", clone["go"])
	}
	if clone["ts"] != 5 {
		t.Errorf("clone[ts] = %d, want 5", clone["ts"])
	}
	if clone["py"] != 3 {
		t.Errorf("clone[py] = %d, want 3", clone["py"])
	}

	// Modification of clone should not affect original
	clone["go"] = 999
	if input["go"] != 10 {
		t.Error("clone modification affected original map")
	}
}

func TestCloneCountMap_empty(t *testing.T) {
	result := cloneCountMap(nil)
	if result != nil {
		t.Errorf("cloneCountMap(nil) = %v, want nil", result)
	}

	result = cloneCountMap(map[string]int64{})
	if result != nil {
		t.Errorf("cloneCountMap({{}}) = %v, want nil", result)
	}
}

func TestMergeCountMaps(t *testing.T) {
	dst := map[string]int64{"go": 10, "ts": 5}
	src := map[string]int64{"go": 5, "py": 3, "": 1, "ts": 2}

	mergeCountMaps(dst, src)

	if dst["go"] != 15 {
		t.Errorf("dst[go] = %d, want 15", dst["go"])
	}
	if dst["ts"] != 7 {
		t.Errorf("dst[ts] = %d, want 7", dst["ts"])
	}
	if dst["py"] != 3 {
		t.Errorf("dst[py] = %d, want 3", dst["py"])
	}
}

func TestMergeCountMaps_skipsEmptyAndZero(t *testing.T) {
	dst := map[string]int64{"go": 10}
	src := map[string]int64{"": 999, "empty_value": 0}

	mergeCountMaps(dst, src)

	if dst["go"] != 10 {
		t.Errorf("dst[go] = %d, want 10 (unchanged)", dst["go"])
	}
	if _, ok := dst[""]; ok {
		t.Error("empty key should not be added")
	}
	if dst["empty_value"] != 0 {
		t.Error("zero count should not be added")
	}
}

func TestMergeCountMaps_srcNil(t *testing.T) {
	dst := map[string]int64{"go": 10}
	mergeCountMaps(dst, nil)
	if dst["go"] != 10 {
		t.Error("nil src should not modify dst")
	}
}

func TestMergeCountMaps_dstNil(t *testing.T) {
	// Merging into nil dst panics - this is expected behavior
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic when merging into nil dst")
		}
	}()
	mergeCountMaps(nil, map[string]int64{"go": 10})
}
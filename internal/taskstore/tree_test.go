package taskstore

import (
	"testing"

	"github.com/dontfuckmycode/dfmc/internal/supervisor"
)

func TestGetTree(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	// Build a 3-level tree:
	// root -> child1 -> leaf1
	//            -> leaf2
	//      -> child2
	tasks := []*supervisor.Task{
		{ID: "root", Title: "root", ParentID: "", Depth: 0},
		{ID: "child1", Title: "child1", ParentID: "root", Depth: 1},
		{ID: "child2", Title: "child2", ParentID: "root", Depth: 1},
		{ID: "leaf1", Title: "leaf1", ParentID: "child1", Depth: 2},
		{ID: "leaf2", Title: "leaf2", ParentID: "child1", Depth: 2},
	}
	for _, tk := range tasks {
		if err := s.SaveTask(tk); err != nil {
			t.Fatalf("SaveTask %s: %v", tk.ID, err)
		}
	}

	tree, err := s.GetTree("root")
	if err != nil {
		t.Fatalf("GetTree root: %v", err)
	}
	if len(tree) != 5 {
		t.Errorf("got %d nodes, want 5", len(tree))
	}
	// Verify breadth-first order: root first
	if tree[0].ID != "root" {
		t.Errorf("tree[0] = %q; want root", tree[0].ID)
	}
	// children should appear before leaves
	ids := make(map[string]int)
	for i, n := range tree {
		ids[n.ID] = i
	}
	if ids["child1"] > ids["leaf1"] {
		t.Error("child1 should appear before leaf1 (BFS order)")
	}

	// Non-existent root returns nil
	nilTree, err := s.GetTree("does-not-exist")
	if err != nil {
		t.Fatalf("GetTree non-existent: %v", err)
	}
	if nilTree != nil {
		t.Error("expected nil for non-existent root")
	}
}

func TestGetAncestors(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	tasks := []*supervisor.Task{
		{ID: "root", Title: "root", ParentID: ""},
		{ID: "child", Title: "child", ParentID: "root"},
		{ID: "leaf", Title: "leaf", ParentID: "child"},
	}
	for _, tk := range tasks {
		if err := s.SaveTask(tk); err != nil {
			t.Fatalf("SaveTask %s: %v", tk.ID, err)
		}
	}

	ancestors, err := s.GetAncestors("leaf")
	if err != nil {
		t.Fatalf("GetAncestors leaf: %v", err)
	}
	if len(ancestors) != 3 {
		t.Errorf("got %d ancestors, want 3", len(ancestors))
	}
	// Should be root-first
	if ancestors[0].ID != "root" {
		t.Errorf("ancestors[0] = %q; want root", ancestors[0].ID)
	}
	if ancestors[2].ID != "leaf" {
		t.Errorf("ancestors[2] = %q; want leaf", ancestors[2].ID)
	}

	// Root has only itself as ancestor
	rootAncestors, err := s.GetAncestors("root")
	if err != nil {
		t.Fatalf("GetAncestors root: %v", err)
	}
	if len(rootAncestors) != 1 {
		t.Errorf("root ancestors: got %d, want 1", len(rootAncestors))
	}

	// Non-existent task
	_, err = s.GetAncestors("ghost")
	if err != nil {
		t.Fatalf("GetAncestors ghost: %v", err)
	}
}

func TestGetRoot(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	s.SaveTask(&supervisor.Task{ID: "root", Title: "root", ParentID: ""})
	s.SaveTask(&supervisor.Task{ID: "child", Title: "child", ParentID: "root"})
	s.SaveTask(&supervisor.Task{ID: "leaf", Title: "leaf", ParentID: "child"})

	root, err := s.GetRoot("leaf")
	if err != nil {
		t.Fatalf("GetRoot leaf: %v", err)
	}
	if root == nil {
		t.Fatal("expected non-nil root")
	}
	if root.ID != "root" {
		t.Errorf("root.ID = %q; want root", root.ID)
	}

	// Root's root is itself
	rootRoot, err := s.GetRoot("root")
	if err != nil {
		t.Fatalf("GetRoot root: %v", err)
	}
	if rootRoot.ID != "root" {
		t.Errorf("root root = %q; want root", rootRoot.ID)
	}
}

func TestValidateTree(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	// Valid tree
	s.SaveTask(&supervisor.Task{ID: "a", Title: "a", ParentID: ""})
	s.SaveTask(&supervisor.Task{ID: "b", Title: "b", ParentID: "a"})

	invalid, err := s.ValidateTree()
	if err != nil {
		t.Fatalf("ValidateTree valid: %v", err)
	}
	if len(invalid) != 0 {
		t.Errorf("expected no invalid tasks, got %v", invalid)
	}

	// Orphan: parentID points to non-existent task
	s.SaveTask(&supervisor.Task{ID: "orphan", Title: "orphan", ParentID: "non-existent"})
	invalid, err = s.ValidateTree()
	if err != nil {
		t.Fatalf("ValidateTree orphan: %v", err)
	}
	if len(invalid) != 1 || invalid[0] != "orphan" {
		t.Errorf("invalid = %v; want [orphan]", invalid)
	}
}

func TestComputeDepths(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	// Build a tree with no depths set
	tasks := []*supervisor.Task{
		{ID: "root", Title: "root", ParentID: ""},
		{ID: "c1", Title: "c1", ParentID: "root"},
		{ID: "c2", Title: "c2", ParentID: "root"},
		{ID: "gc1", Title: "gc1", ParentID: "c1"},
	}
	for _, tk := range tasks {
		if err := s.SaveTask(tk); err != nil {
			t.Fatalf("SaveTask %s: %v", tk.ID, err)
		}
	}

	err := s.ComputeDepths()
	if err != nil {
		t.Fatalf("ComputeDepths: %v", err)
	}

	// Verify depths
	for _, id := range []string{"root", "c1", "c2", "gc1"} {
		tk, _ := s.LoadTask(id)
		want := map[string]int{"root": 0, "c1": 1, "c2": 1, "gc1": 2}[id]
		if tk.Depth != want {
			t.Errorf("%s depth = %d; want %d", id, tk.Depth, want)
		}
	}
}

func TestGetChildren(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	s.SaveTask(&supervisor.Task{ID: "root", Title: "root", ParentID: ""})
	s.SaveTask(&supervisor.Task{ID: "c1", Title: "c1", ParentID: "root", Order: 0})
	s.SaveTask(&supervisor.Task{ID: "c2", Title: "c2", ParentID: "root", Order: 1})
	s.SaveTask(&supervisor.Task{ID: "c3", Title: "c3", ParentID: "root", Order: 2})
	s.SaveTask(&supervisor.Task{ID: "other", Title: "other", ParentID: ""})

	children, err := s.GetChildren("root")
	if err != nil {
		t.Fatalf("GetChildren root: %v", err)
	}
	if len(children) != 3 {
		t.Fatalf("got %d children, want 3", len(children))
	}
	// Should be sorted by Order
	if children[0].ID != "c1" || children[1].ID != "c2" || children[2].ID != "c3" {
		t.Errorf("children order wrong: %v", children)
	}

	// No children for non-existent parent
	none, err := s.GetChildren("ghost")
	if err != nil {
		t.Fatalf("GetChildren ghost: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("got %d children for ghost, want 0", len(none))
	}
}

func TestOnTaskBlocked(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	// t1 blocks t2, t2 blocks t3
	s.SaveTask(&supervisor.Task{ID: "t1", Title: "t1", DependsOn: []string{}})
	s.SaveTask(&supervisor.Task{ID: "t2", Title: "t2", DependsOn: []string{"t1"}, BlockedBy: []string{}})
	s.SaveTask(&supervisor.Task{ID: "t3", Title: "t3", DependsOn: []string{"t2"}, BlockedBy: []string{}})

	err := s.OnTaskBlocked("t1")
	if err != nil {
		t.Fatalf("OnTaskBlocked t1: %v", err)
	}

	t2, _ := s.LoadTask("t2")
	if len(t2.BlockedBy) != 1 || t2.BlockedBy[0] != "t1" {
		t.Errorf("t2.BlockedBy = %v; want [t1]", t2.BlockedBy)
	}
	// t3 should not be blocked by t1 (indirect dependency)
	t3, _ := s.LoadTask("t3")
	if len(t3.BlockedBy) != 0 {
		t.Errorf("t3.BlockedBy = %v; want []", t3.BlockedBy)
	}

	// Blocking a non-existent task should not error
	err = s.OnTaskBlocked("non-existent")
	if err != nil {
		t.Fatalf("OnTaskBlocked non-existent: %v", err)
	}
}

func TestOnTaskUnblocked(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	s.SaveTask(&supervisor.Task{ID: "t1", Title: "t1", BlockedBy: []string{"x", "y"}})
	s.SaveTask(&supervisor.Task{ID: "t2", Title: "t2", BlockedBy: []string{"x"}})

	err := s.OnTaskUnblocked("x")
	if err != nil {
		t.Fatalf("OnTaskUnblocked x: %v", err)
	}

	t1, _ := s.LoadTask("t1")
	if len(t1.BlockedBy) != 1 || t1.BlockedBy[0] != "y" {
		t.Errorf("t1.BlockedBy = %v; want [y]", t1.BlockedBy)
	}

	t2, _ := s.LoadTask("t2")
	if len(t2.BlockedBy) != 0 {
		t.Errorf("t2.BlockedBy = %v; want []", t2.BlockedBy)
	}
}

func TestListTasks_RootOnly(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	s.SaveTask(&supervisor.Task{ID: "root1", Title: "root1", ParentID: ""})
	s.SaveTask(&supervisor.Task{ID: "root2", Title: "root2", ParentID: ""})
	s.SaveTask(&supervisor.Task{ID: "child1", Title: "child1", ParentID: "root1"})

	roots, err := s.ListTasks(ListOptions{RootOnly: true})
	if err != nil {
		t.Fatalf("ListTasks RootOnly: %v", err)
	}
	if len(roots) != 2 {
		t.Errorf("got %d roots, want 2", len(roots))
	}
	for _, tk := range roots {
		if tk.ParentID != "" {
			t.Errorf("non-root task %s in RootOnly list", tk.ID)
		}
	}
}

func TestListTasks_DepthFilter(t *testing.T) {
	db := tempDB(t)
	s := NewStore(db)

	// All depth 0 - uninitialized
	s.SaveTask(&supervisor.Task{ID: "r", Title: "r", ParentID: ""})
	s.SaveTask(&supervisor.Task{ID: "c", Title: "c", ParentID: "r"})
	s.SaveTask(&supervisor.Task{ID: "gc", Title: "gc", ParentID: "c"})

	// Run ComputeDepths to populate
	s.ComputeDepths()

	// MinDepth=1 should skip root
	deep, err := s.ListTasks(ListOptions{MinDepth: 1})
	if err != nil {
		t.Fatalf("ListTasks MinDepth: %v", err)
	}
	for _, tk := range deep {
		if tk.Depth < 1 {
			t.Errorf("task %s depth=%d, want >=1", tk.ID, tk.Depth)
		}
	}

	// MaxDepth=1 should skip grandchild
	shallow, err := s.ListTasks(ListOptions{MaxDepth: 1})
	if err != nil {
		t.Fatalf("ListTasks MaxDepth: %v", err)
	}
	for _, tk := range shallow {
		if tk.Depth > 1 {
			t.Errorf("task %s depth=%d, want <=1", tk.ID, tk.Depth)
		}
	}
}
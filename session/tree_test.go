package session

import (
	"math/rand"
	"os"
	"testing"
)

func TestSession_TRE01_BranchRootToLeaf(t *testing.T) {
	s, _ := openStore(t, Options{})
	var ids []string
	for i := 0; i < 5; i++ {
		e, _ := s.Append(userEntry(string(rune('a' + i))))
		ids = append(ids, e.ID)
	}
	branch, err := s.Branch("")
	if err != nil {
		t.Fatalf("Branch: %v", err)
	}
	if len(branch) != 5 {
		t.Fatalf("branch = %d entries, want 5", len(branch))
	}
	for i, e := range branch {
		if e.ID != ids[i] {
			t.Fatalf("branch[%d] = %s, want %s", i, e.ID, ids[i])
		}
	}
}

func TestSession_TRE02_BranchMiddle(t *testing.T) {
	s, _ := openStore(t, Options{})
	var ids []string
	for i := 0; i < 5; i++ {
		e, _ := s.Append(userEntry("x"))
		ids = append(ids, e.ID)
	}
	branch, err := s.Branch(ids[2])
	if err != nil {
		t.Fatalf("Branch: %v", err)
	}
	if len(branch) != 3 || branch[2].ID != ids[2] {
		t.Fatalf("branch = %d, want the prefix to ids[2]", len(branch))
	}
}

func TestSession_TRE03_UnknownLeaf(t *testing.T) {
	s, _ := openStore(t, Options{})
	if _, err := s.Branch("nope"); err == nil {
		t.Fatal("Branch of an unknown leaf should error")
	}
}

func TestSession_TRE04_ChildrenOrder(t *testing.T) {
	s, _ := openStore(t, Options{})
	root, _ := s.Append(userEntry("root"))
	first, _ := s.AppendOn(root.ID, userEntry("first"))
	second, _ := s.AppendOn(root.ID, userEntry("second"))
	kids := s.Children(root.ID)
	if len(kids) != 2 || kids[0] != first.ID || kids[1] != second.ID {
		t.Fatalf("children = %v, want file order", kids)
	}
}

func TestSession_TRE05_Roots(t *testing.T) {
	s, _ := openStore(t, Options{})
	a, _ := s.AppendOn("", userEntry("a"))
	b, _ := s.AppendOn("", userEntry("b"))
	roots := s.Children("")
	if len(roots) != 2 || roots[0] != a.ID || roots[1] != b.ID {
		t.Fatalf("roots = %v, want both", roots)
	}
}

func TestSession_TRE06_NavigateThenAppend(t *testing.T) {
	s, _ := openStore(t, Options{})
	a, _ := s.Append(userEntry("a"))
	b, _ := s.Append(assistantEntry("b"))
	c, _ := s.Append(userEntry("c"))
	if err := s.SetLeaf(b.ID); err != nil {
		t.Fatalf("SetLeaf: %v", err)
	}
	d, _ := s.Append(assistantEntry("d"))
	if d.Parent != b.ID {
		t.Fatalf("d.Parent = %q, want %q", d.Parent, b.ID)
	}
	if _, err := s.Branch(c.ID); err != nil {
		t.Fatalf("old leaf unreachable: %v", err)
	}
	_ = a
}

func TestSession_TRE07_SetLeafUnknown(t *testing.T) {
	s, _ := openStore(t, Options{})
	if err := s.SetLeaf("nope"); err == nil {
		t.Fatal("SetLeaf of an unknown id should error")
	}
}

func TestSession_TRE08_SetLeafDoesNotWrite(t *testing.T) {
	s, path := openStore(t, Options{})
	a, _ := s.Append(userEntry("a"))
	before, _ := os.ReadFile(path)
	if err := s.SetLeaf(a.ID); err != nil {
		t.Fatalf("SetLeaf: %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("SetLeaf must not write to the file")
	}
}

func TestSession_TRE09_BranchIsAPath(t *testing.T) {
	s, _ := openStore(t, Options{})
	rng := rand.New(rand.NewSource(11))
	var ids []string
	for i := 0; i < 200; i++ {
		var parent string
		if len(ids) > 0 && rng.Intn(3) == 0 {
			parent = ids[rng.Intn(len(ids))]
		}
		var e Entry
		if rng.Intn(2) == 0 {
			e = userEntry("u")
		} else {
			e = assistantEntry("a")
		}
		if parent == "" {
			ne, err := s.Append(e)
			if err != nil {
				t.Fatalf("Append: %v", err)
			}
			ids = append(ids, ne.ID)
		} else {
			ne, err := s.AppendOn(parent, e)
			if err != nil {
				t.Fatalf("AppendOn: %v", err)
			}
			ids = append(ids, ne.ID)
		}
		if rng.Intn(4) == 0 {
			_ = s.SetLeaf(ids[rng.Intn(len(ids))])
		}
		branch, err := s.Branch("")
		if err != nil {
			t.Fatalf("Branch: %v", err)
		}
		for j, be := range branch {
			if j == 0 {
				continue
			}
			if be.Parent != branch[j-1].ID {
				t.Fatalf("branch is not a chain at %d: %s -> %s", j, be.Parent, branch[j-1].ID)
			}
		}
		if len(branch) > 0 && branch[0].Parent != "" {
			t.Fatalf("branch root %s has parent %s", branch[0].ID, branch[0].Parent)
		}
	}
}

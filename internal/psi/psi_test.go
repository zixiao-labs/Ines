package psi

import "testing"

func TestWalkVisitsEveryNodeInPreOrder(t *testing.T) {
	source := []byte("package x\nfunc Hello() {}\n")
	root := NewFile("a.go", "go", source)
	fn := NewElement(KindFunction, "Hello", Range{Start: 10, End: 25}, source, "go")
	root.AddChild(fn)
	param := NewElement(KindParameter, "_", Range{Start: 18, End: 19}, source, "go")
	fn.AddChild(param)

	var seen []Kind
	Walk(root, VisitorFunc(func(e Element) {
		seen = append(seen, e.Kind())
	}))
	want := []Kind{KindFile, KindFunction, KindParameter}
	if len(seen) != len(want) {
		t.Fatalf("seen %v want %v", seen, want)
	}
	for i, k := range want {
		if seen[i] != k {
			t.Fatalf("step %d: got %s want %s", i, seen[i], k)
		}
	}
}

func TestFindByKindFiltersDescendants(t *testing.T) {
	source := []byte("class A {}\nclass B {}\n")
	root := NewFile("a.ts", "typescript", source)
	a := NewElement(KindClass, "A", Range{Start: 0, End: 10}, source, "typescript")
	b := NewElement(KindClass, "B", Range{Start: 11, End: 21}, source, "typescript")
	root.AddChild(a)
	root.AddChild(b)

	classes := FindByKind(root, KindClass)
	if len(classes) != 2 {
		t.Fatalf("expected 2 classes, got %d", len(classes))
	}
	if classes[0].Name() != "A" || classes[1].Name() != "B" {
		t.Fatalf("unexpected names: %s %s", classes[0].Name(), classes[1].Name())
	}
}

func TestBaseElementTextHandlesOutOfRange(t *testing.T) {
	source := []byte("hello")
	el := NewElement(KindUnknown, "x", Range{Start: 2, End: 100}, source, "go")
	if got := el.Text(); got != "llo" {
		t.Fatalf("clamped text: got %q want %q", got, "llo")
	}
}

package psi

// FindByKind returns every descendant of root (root included) whose Kind
// matches kind. The traversal order is depth-first pre-order.
func FindByKind(root Element, kind Kind) []Element {
	var out []Element
	Walk(root, VisitorFunc(func(e Element) {
		if e.Kind() == kind {
			out = append(out, e)
		}
	}))
	return out
}

// FindByName returns every descendant of root (root included) whose Name
// matches name. Useful for symbol lookups during navigation.
func FindByName(root Element, name string) []Element {
	var out []Element
	Walk(root, VisitorFunc(func(e Element) {
		if e.Name() == name {
			out = append(out, e)
		}
	}))
	return out
}

// FirstAncestorOfKind walks up from element looking for the first parent
// whose Kind matches kind. It returns nil when no such ancestor exists.
func FirstAncestorOfKind(element Element, kind Kind) Element {
	for cur := element.Parent(); cur != nil; cur = cur.Parent() {
		if cur.Kind() == kind {
			return cur
		}
	}
	return nil
}

// CountElements returns the total number of nodes in the subtree rooted at
// root. Used by metrics reporting and for sanity-checks in tests.
func CountElements(root Element) int {
	count := 0
	Walk(root, VisitorFunc(func(_ Element) { count++ }))
	return count
}

package psi

// Visitor mirrors JetBrains' PsiElementVisitor. Implementations override
// VisitElement to receive every node and can perform their own dispatch based
// on Kind() to handle particular element types.
type Visitor interface {
	VisitElement(element Element)
}

// VisitorFunc adapts an ordinary function so it can be used wherever a
// Visitor is accepted.
type VisitorFunc func(element Element)

// VisitElement satisfies Visitor.
func (f VisitorFunc) VisitElement(element Element) { f(element) }

// Walk performs a depth-first pre-order traversal starting at root and
// invoking visitor for every reachable element, including root itself.
func Walk(root Element, visitor Visitor) {
	if root == nil || visitor == nil {
		return
	}
	visitor.VisitElement(root)
	for _, child := range root.Children() {
		Walk(child, visitor)
	}
}

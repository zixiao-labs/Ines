// Package psi defines the Program Structure Interface used to wrap the syntax
// tree produced by the parser layer. It is modelled on JetBrains' PSI: every
// node in the source tree implements Element, and language-specific element
// kinds (PsiClass, PsiMethod, PsiParameter, ...) compose the basic tree
// operations exposed here.
//
// PSI is intentionally separated from the parser: the parser hands back an
// abstract syntax tree, then a language adapter walks it and constructs PSI
// nodes that carry behavioural capabilities (navigation, queries, edits) on
// top of the structural information.
package psi

// Range describes a half-open byte interval inside the source file.
type Range struct {
	Start int
	End   int
}

// Position is a 1-based line/column pair plus the absolute byte offset.
type Position struct {
	Line   int
	Column int
	Offset int
}

// Kind enumerates the well-known PSI node categories. Language adapters reuse
// these constants when they emit a tree so that downstream consumers (code
// completion, refactoring, navigation) can speak a shared vocabulary.
type Kind string

const (
	KindFile       Kind = "file"
	KindClass      Kind = "class"
	KindInterface  Kind = "interface"
	KindStruct     Kind = "struct"
	KindEnum       Kind = "enum"
	KindMethod     Kind = "method"
	KindFunction   Kind = "function"
	KindParameter  Kind = "parameter"
	KindField      Kind = "field"
	KindVariable   Kind = "variable"
	KindImport     Kind = "import"
	KindPackage    Kind = "package"
	KindNamespace  Kind = "namespace"
	KindExpression Kind = "expression"
	KindStatement  Kind = "statement"
	KindUnknown    Kind = "unknown"
)

// Element is the core interface implemented by every PSI node. The contract
// mirrors the JetBrains PsiElement: navigation up and down the tree, source
// range information, and the canonical text slice the node was built from.
type Element interface {
	// Kind returns the categorical identifier for the node.
	Kind() Kind
	// Name returns the identifier carried by the node, or the empty string for
	// anonymous constructs.
	Name() string
	// Range returns the half-open byte interval the node spans inside its file.
	Range() Range
	// Text returns the verbatim source slice covered by Range.
	Text() string
	// Parent returns the enclosing PSI element or nil when this is the root.
	Parent() Element
	// Children returns a snapshot of the direct child elements in source order.
	Children() []Element
	// Language returns the language identifier the element was produced for
	// (e.g. "go", "typescript", "rust"). The value is stable across the tree.
	Language() string
}

// File is the root of every PSI tree. It always represents an entire source
// file and exposes the absolute path on disk so consumers can correlate PSI
// nodes back to filesystem locations.
type File interface {
	Element
	Path() string
}

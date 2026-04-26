package psi

// BaseElement is a reusable struct that satisfies Element and is intended to
// be embedded by language-specific node types. It owns the structural data
// (kind, name, range, parent/children, language) so that language adapters
// only need to add behavioural methods on top.
type BaseElement struct {
	kind     Kind
	name     string
	rng      Range
	source   []byte
	parent   Element
	children []Element
	language string
}

// NewElement constructs a BaseElement and is the lowest-level constructor used
// by the language-specific factories.
func NewElement(kind Kind, name string, rng Range, source []byte, language string) *BaseElement {
	return &BaseElement{
		kind:     kind,
		name:     name,
		rng:      rng,
		source:   source,
		language: language,
	}
}

func (b *BaseElement) Kind() Kind     { return b.kind }
func (b *BaseElement) Name() string   { return b.name }
func (b *BaseElement) Range() Range   { return b.rng }
func (b *BaseElement) Parent() Element {
	if b == nil {
		return nil
	}
	return b.parent
}
func (b *BaseElement) Language() string { return b.language }

// Text returns the source slice the element was built from. It clamps the
// range so that callers cannot panic if a buggy parser produced a range that
// exceeds the source length.
func (b *BaseElement) Text() string {
	if b == nil || b.source == nil {
		return ""
	}
	start := clamp(b.rng.Start, 0, len(b.source))
	end := clamp(b.rng.End, start, len(b.source))
	return string(b.source[start:end])
}

// Children returns a defensive copy so callers can iterate freely without
// risking mutation of the underlying tree.
func (b *BaseElement) Children() []Element {
	if b == nil || len(b.children) == 0 {
		return nil
	}
	out := make([]Element, len(b.children))
	copy(out, b.children)
	return out
}

// AddChild appends a child element and wires its Parent back to b. It is
// intended for use by language-specific factories during construction.
func (b *BaseElement) AddChild(child Element) {
	if child == nil {
		return
	}
	if base, ok := child.(*BaseElement); ok {
		base.parent = b
	}
	b.children = append(b.children, child)
}

// SetParent overrides the parent pointer. Useful when an element is moved
// during refactoring transformations.
func (b *BaseElement) SetParent(parent Element) {
	b.parent = parent
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// BaseFile is the concrete File implementation that language adapters embed.
type BaseFile struct {
	*BaseElement
	path string
}

// NewFile constructs a BaseFile rooted at the given absolute path.
func NewFile(path string, language string, source []byte) *BaseFile {
	return &BaseFile{
		BaseElement: NewElement(KindFile, path, Range{Start: 0, End: len(source)}, source, language),
		path:        path,
	}
}

func (f *BaseFile) Path() string { return f.path }

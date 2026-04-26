// Package c registers a shared adapter for C and C++. The two languages have
// significantly different grammars but share enough surface syntax (preprocessor
// directives, function signatures, struct/class declarations) that the
// bootstrap regex parser can serve both well enough for outline/navigation.
// The tree-sitter migration will split them into distinct adapters.
package c

import (
	"regexp"

	"github.com/zixiao-labs/ines/internal/lang"
	"github.com/zixiao-labs/ines/internal/lang/regexparser"
	"github.com/zixiao-labs/ines/internal/psi"
)

func init() {
	rules := []regexparser.Rule{
		regexparser.MustRule(psi.KindImport, `^\s*#\s*include\s+[<"]([^>"]+)[>"]`),
		regexparser.MustRule(psi.KindNamespace, `^\s*namespace\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindClass, `^\s*(?:template\s*<[^>]*>\s*)?class\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindStruct, `^\s*struct\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindEnum, `^\s*enum(?:\s+(?:class|struct))?\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindFunction, `^\s*(?:[A-Za-z_][\w:<>,\s\*&]*\s+)([A-Za-z_][A-Za-z0-9_]*)\s*\([^;]*\)`),
	}
	parser := regexparser.New("cpp", rules, regexp.MustCompile(`^\s*//`))
	lang.Register(&lang.Adapter{
		Language:   "cpp",
		Extensions: []string{".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx"},
		Parser:     parser,
	})
}

// Package typescript registers the TypeScript / JavaScript adapter.
package typescript

import (
	"regexp"

	"github.com/zixiao-labs/ines/internal/lang"
	"github.com/zixiao-labs/ines/internal/lang/regexparser"
	"github.com/zixiao-labs/ines/internal/psi"
)

func init() {
	rules := []regexparser.Rule{
		regexparser.MustRule(psi.KindImport, `^\s*import\s+.*?from\s+['"]([^'"]+)['"]`),
		regexparser.MustRule(psi.KindImport, `^\s*import\s+['"]([^'"]+)['"]`),
		regexparser.MustRule(psi.KindClass, `^\s*(?:@[A-Za-z_$][\w.$]*\s+)*(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindInterface, `^\s*(?:export\s+)?interface\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindEnum, `^\s*(?:export\s+)?(?:const\s+)?enum\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindFunction, `^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s*\*?\s*([A-Za-z_$][A-Za-z0-9_$]*)\s*\(`),
		regexparser.MustRule(psi.KindFunction, `^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?::[^=]+)?\s*=\s*(?:async\s*)?\([^)]*\)\s*=>`),
		regexparser.MustRule(psi.KindVariable, `^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*(?::[^=]+)?\s*=\s*[^(=]`),
	}
	parser := regexparser.New("typescript", rules, regexp.MustCompile(`^\s*//`))
	lang.Register(&lang.Adapter{
		Language:   "typescript",
		Extensions: []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"},
		Parser:     parser,
	})
}

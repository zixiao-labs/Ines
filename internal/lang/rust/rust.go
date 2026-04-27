// Package rust registers the Rust language adapter.
package rust

import (
	"regexp"

	"github.com/zixiao-labs/ines/internal/lang"
	"github.com/zixiao-labs/ines/internal/lang/regexparser"
	"github.com/zixiao-labs/ines/internal/psi"
)

func init() {
	rules := []regexparser.Rule{
		regexparser.MustRule(psi.KindImport, `^\s*use\s+([A-Za-z_][A-Za-z0-9_:]*)`),
		regexparser.MustRule(psi.KindNamespace, `^\s*(?:pub(?:\([^)]+\))?\s+)?mod\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindStruct, `^\s*(?:pub(?:\([^)]+\))?\s+)?struct\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindEnum, `^\s*(?:pub(?:\([^)]+\))?\s+)?enum\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindInterface, `^\s*(?:pub(?:\([^)]+\))?\s+)?trait\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindFunction, `^\s*(?:pub(?:\([^)]+\))?\s+)?(?:async\s+)?(?:unsafe\s+)?(?:extern\s+(?:"[^"]+"\s+)?)?fn\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindVariable, `^\s*(?:pub(?:\([^)]+\))?\s+)?(?:static|const)\s+([A-Z_][A-Z0-9_]*)`),
	}
	parser := regexparser.New("rust", rules, regexp.MustCompile(`^\s*//`))
	lang.Register(&lang.Adapter{
		Language:   "rust",
		Extensions: []string{".rs"},
		Parser:     parser,
	})
}

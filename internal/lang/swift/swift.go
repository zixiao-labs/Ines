// Package swift registers the Swift language adapter.
package swift

import (
	"regexp"

	"github.com/zixiao-labs/ines/internal/lang"
	"github.com/zixiao-labs/ines/internal/lang/regexparser"
	"github.com/zixiao-labs/ines/internal/psi"
)

func init() {
	rules := []regexparser.Rule{
		regexparser.MustRule(psi.KindImport, `^\s*import\s+([A-Za-z_][A-Za-z0-9_.]*)`),
		regexparser.MustRule(psi.KindClass, `^\s*(?:public\s+|internal\s+|private\s+|fileprivate\s+|open\s+|final\s+)*class\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindStruct, `^\s*(?:public\s+|internal\s+|private\s+|fileprivate\s+)*struct\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindEnum, `^\s*(?:public\s+|internal\s+|private\s+|fileprivate\s+|indirect\s+)*enum\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindInterface, `^\s*(?:public\s+|internal\s+|private\s+|fileprivate\s+)*protocol\s+([A-Za-z_][A-Za-z0-9_]*)`),
		regexparser.MustRule(psi.KindFunction, `^\s*(?:public\s+|internal\s+|private\s+|fileprivate\s+|static\s+|class\s+|override\s+)*func\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`),
	}
	parser := regexparser.New("swift", rules, regexp.MustCompile(`^\s*//`))
	lang.Register(&lang.Adapter{
		Language:   "swift",
		Extensions: []string{".swift"},
		Parser:     parser,
	})
}

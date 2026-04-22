# Prompt For AI Agent

Ines implements JetBrains PSI using Go and Tree-sitter.

Implementation: First, lexical analysis is performed, followed by parsing: a tree-sitter is used to construct an AST (Abstract Syntax Tree) from the token sequence.

Then, PSI wrapping is performed: the AST nodes are wrapped into PSI elements with behavioral capabilities (such as PsiClass, PsiMethod, PsiParameter, etc.), supporting operations such as navigation, modification, and querying.

1. PSI Tree Structure

PSI represents the source code as a hierarchical node tree, where each node is a PsiElement. The root node of the tree is usually PsiFile, representing the entire source file.  For example:

PsiFile → PsiClass → PsiMethod → ​​PsiParameter → PsiExpression

2. Key Components

PsiElement: The core interface implemented by PSI node structs, providing basic tree operations (such as getting parent nodes, child nodes, text ranges, etc.).

PsiFile: Represents the entire source file and is the root node of the PSI tree.

PsiTreeUtil: A utility class used to navigate and find nodes of specific types in the PSI tree.

PsiElementVisitor: A visitor pattern used to traverse the PSI tree and perform specific operations.

IDE Communication: Uses a high-performance Protobuf interface.

Includes dependencies during indexing and avoids lazy loading.

Supports reporting of memory usage, average generation time, and CPU utilization via IPC interface.

Requires support for syntax highlighting, intelligent code completion, safe refactoring, and problem diagnosis in C, C++, Java, Javascript/TypeScript, Swift, and Rust.
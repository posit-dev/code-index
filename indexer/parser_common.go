// Copyright (C) 2026 by Posit Software, PBC
package indexer

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

const maxSignatureLen = 200
const maxReturnLen = 200

func truncateSignature(sig string) string {
	if len(sig) <= maxSignatureLen {
		return sig
	}
	return sig[:maxSignatureLen] + "..."
}

// extractBodyInfo extracts return expressions and deduplicated callee
// names from a function_definition node's body. Works with any
// tree-sitter grammar that uses return_statement and call_expression
// node types (C, C++, and others).
func extractBodyInfo(
	node *sitter.Node,
	content []byte,
) (returns []string, calls []string) {
	body := node.ChildByFieldName("body")
	if body == nil {
		return nil, nil
	}

	seen := make(map[string]bool)
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		for i := 0; i < int(n.ChildCount()); i++ {
			child := n.Child(i)
			nodeType := child.Type()

			if nodeType == "function_definition" {
				continue
			}

			switch nodeType {
			case "return_statement":
				text := child.Content(content)
				expr := strings.TrimSpace(
					strings.TrimSuffix(
						strings.TrimSpace(
							strings.TrimPrefix(text, "return"),
						),
						";",
					),
				)
				if expr == "" {
					break
				}
				if len(expr) > maxReturnLen {
					expr = expr[:maxReturnLen] + "..."
				}
				returns = append(returns, expr)

			case "call_expression":
				callee := child.ChildByFieldName("function")
				if callee != nil {
					name := callee.Content(content)
					if !seen[name] {
						seen[name] = true
						calls = append(calls, name)
					}
				}
			}

			walk(child)
		}
	}
	walk(body)

	return returns, calls
}

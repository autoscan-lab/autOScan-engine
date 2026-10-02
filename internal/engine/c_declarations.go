package engine

import sitter "github.com/smacker/go-tree-sitter"

type declarationPattern struct {
	kind, label string
	node        *sitter.Node
}

func declaratorChain(node *sitter.Node) []*sitter.Node {
	var chain []*sitter.Node
	for node != nil {
		chain = append(chain, node)
		node = node.ChildByFieldName("declarator")
	}
	return chain
}

func declarationHasName(node *sitter.Node, name string, content []byte) bool {
	for i := 0; i < int(node.ChildCount()); i++ {
		if node.FieldNameForChild(i) == "declarator" && nameOf(node.Child(i), content) == name {
			return true
		}
	}
	return false
}

// Only visible object/parameter names establish runtime bounds; unknown header macros abstain.
func runtimeName(name string, at *sitter.Node, content []byte) bool {
	for scope := at.Parent(); scope != nil; scope = scope.Parent() {
		switch scope.Type() {
		case "compound_statement", "translation_unit", "for_statement":
			for i := int(scope.NamedChildCount()) - 1; i >= 0; i-- {
				child := scope.NamedChild(i)
				if child.StartByte() >= at.StartByte() {
					continue
				}
				if child.Type() == "declaration" && declarationHasName(child, name, content) {
					return true
				}
			}
		case "function_definition":
			for _, part := range declaratorChain(scope.ChildByFieldName("declarator")) {
				if parameters := part.ChildByFieldName("parameters"); parameters != nil {
					for i := 0; i < int(parameters.NamedChildCount()); i++ {
						param := parameters.NamedChild(i)
						if nameOf(param.ChildByFieldName("declarator"), content) == name {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func runtimeBound(node *sitter.Node, content []byte) bool {
	if node == nil || node.Type() == "sizeof_expression" {
		return false
	}
	if node.Type() == "identifier" {
		return runtimeName(node.Content(content), node, content)
	}
	if node.Type() == "call_expression" {
		function := node.ChildByFieldName("function")
		root := node
		for root.Parent() != nil {
			root = root.Parent()
		}
		if function != nil {
			for i := 0; i < int(root.NamedChildCount()); i++ {
				child := root.NamedChild(i)
				if child.Type() == "preproc_function_def" {
					if name := child.ChildByFieldName("name"); name != nil && name.Content(content) == function.Content(content) {
						return false
					}
				}
			}
		}
		return true
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		if runtimeBound(node.NamedChild(i), content) {
			return true
		}
	}
	return false
}

func declarationPatterns(root *sitter.Node, content []byte) []declarationPattern {
	if root.HasError() {
		return nil
	}
	var patterns []declarationPattern
	var visit func(*sitter.Node)
	visit = func(node *sitter.Node) {
		if node.Type() == "preproc_def" || node.Type() == "preproc_function_def" {
			return
		}
		if node.Type() == "declaration" || node.Type() == "type_definition" {
			for i := 0; i < int(node.ChildCount()); i++ {
				if node.FieldNameForChild(i) != "declarator" {
					continue
				}
				declarator := node.Child(i)
				chain := declaratorChain(declarator)
				arrays := 0
				var dynamic *sitter.Node
				for _, part := range chain {
					if part.Type() == "array_declarator" {
						arrays++
						if runtimeBound(part.ChildByFieldName("size"), content) {
							dynamic = part
						}
					}
				}
				if dynamic != nil {
					patterns = append(patterns, declarationPattern{"variable_length_array", "Variable-length array", dynamic})
				}
				value := declarator.ChildByFieldName("value")
				if arrays > 0 && value != nil && (value.Type() == "initializer_list" || value.Type() == "string_literal" || value.Type() == "concatenated_string") {
					patterns = append(patterns, declarationPattern{"initialized_array", "Initialized array", declarator})
				}
			}
		}
		for i := 0; i < int(node.NamedChildCount()); i++ {
			visit(node.NamedChild(i))
		}
	}
	visit(root)
	return patterns
}

func sizeofVariable(node *sitter.Node, content []byte) bool {
	value := node.ChildByFieldName("value")
	if value == nil {
		return false
	}
	var variable func(*sitter.Node) bool
	variable = func(part *sitter.Node) bool {
		if part.Type() == "identifier" {
			return runtimeName(part.Content(content), part, content)
		}
		for i := 0; i < int(part.NamedChildCount()); i++ {
			if variable(part.NamedChild(i)) {
				return true
			}
		}
		return false
	}
	return variable(value)
}

func functionDeclarator(node *sitter.Node) *sitter.Node {
	for _, part := range declaratorChain(node) {
		if part.Type() == "function_declarator" {
			inner := part.ChildByFieldName("declarator")
			if inner != nil && inner.Type() == "identifier" {
				return part
			}
		}
	}
	return nil
}

func containsWaitCall(node *sitter.Node, content []byte) bool {
	if node.Type() == "call_expression" {
		function := node.ChildByFieldName("function")
		for function != nil && function.Type() == "parenthesized_expression" {
			function = function.NamedChild(0)
		}
		if function != nil && function.Type() == "identifier" && (function.Content(content) == "wait" || function.Content(content) == "waitpid") {
			return true
		}
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		if containsWaitCall(node.NamedChild(i), content) {
			return true
		}
	}
	return false
}

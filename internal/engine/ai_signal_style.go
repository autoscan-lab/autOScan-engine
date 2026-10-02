package engine

import (
	"fmt"
	sitter "github.com/smacker/go-tree-sitter"
)

func expressionIdentifier(node *sitter.Node, content []byte) string {
	for node != nil {
		switch node.Type() {
		case "identifier":
			return node.Content(content)
		case "parenthesized_expression":
			node = node.NamedChild(0)
		case "cast_expression":
			node = node.ChildByFieldName("value")
		case "pointer_expression":
			node = node.ChildByFieldName("argument")
		default:
			return ""
		}
	}
	return ""
}

func callArguments(node *sitter.Node) []*sitter.Node {
	var arguments []*sitter.Node
	list := node.ChildByFieldName("arguments")
	if list == nil {
		return arguments
	}
	for i := 0; i < int(list.NamedChildCount()); i++ {
		if child := list.NamedChild(i); child.Type() != "comment" {
			arguments = append(arguments, child)
		}
	}
	return arguments
}

// Resolve registration syntax instead of guessing a handler from its name.
func registeredSignalHandlers(root *sitter.Node, content []byte) map[string]bool {
	handlers, actions := map[string]bool{}, map[string]string{}
	var registrations []string
	var visit func(*sitter.Node)
	visit = func(node *sitter.Node) {
		if insideMacro(node) {
			return
		}
		if node.Type() == "assignment_expression" {
			left, right := node.ChildByFieldName("left"), node.ChildByFieldName("right")
			if left != nil && left.Type() == "field_expression" {
				field := left.ChildByFieldName("field")
				if field != nil && (field.Content(content) == "sa_handler" || field.Content(content) == "sa_sigaction") {
					if action, handler := expressionIdentifier(left.ChildByFieldName("argument"), content), expressionIdentifier(right, content); action != "" && handler != "" {
						if key := signalObjectKey(node, action, content); key != "" {
							actions[key] = handler
						}
					}
				}
			}
		}
		if node.Type() == "call_expression" {
			function := expressionIdentifier(node.ChildByFieldName("function"), content)
			arguments := callArguments(node)
			if len(arguments) >= 2 {
				name := expressionIdentifier(arguments[1], content)
				if name != "" {
					switch function {
					case "signal":
						handlers[name] = true
					case "sigaction":
						if key := signalObjectKey(node, name, content); key != "" {
							registrations = append(registrations, key)
						}
					}
				}
			}
		}
		for i := 0; i < int(node.NamedChildCount()); i++ {
			visit(node.NamedChild(i))
		}
	}
	visit(root)
	for _, action := range registrations {
		if handler := actions[action]; handler != "" {
			handlers[handler] = true
		}
	}
	return handlers
}

func signalObjectKey(at *sitter.Node, name string, content []byte) string {
	for scope := at.Parent(); scope != nil; scope = scope.Parent() {
		if scope.Type() != "compound_statement" && scope.Type() != "translation_unit" {
			continue
		}
		for i := int(scope.NamedChildCount()) - 1; i >= 0; i-- {
			child := scope.NamedChild(i)
			if child.StartByte() < at.StartByte() && child.Type() == "declaration" && declarationHasName(child, name, content) {
				return fmt.Sprintf("%d:%s", child.StartByte(), name)
			}
		}
	}
	return ""
}

func hasUnusedAttribute(node *sitter.Node, content []byte) bool {
	if node.Type() == "identifier" && (node.Content(content) == "unused" || node.Content(content) == "__unused__") {
		return true
	}
	for i := 0; i < int(node.NamedChildCount()); i++ {
		if hasUnusedAttribute(node.NamedChild(i), content) {
			return true
		}
	}
	return false
}

func signalStateDeclaration(node *sitter.Node, content []byte) bool {
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if (child.Type() == "type_qualifier" && child.Content(content) == "volatile") || (child.Type() == "type_identifier" && child.Content(content) == "sig_atomic_t") {
			return true
		}
	}
	return false
}

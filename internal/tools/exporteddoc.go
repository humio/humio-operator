package main

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/singlechecker"
)

var Analyzer = &analysis.Analyzer{
	Name: "exporteddoc",
	Doc:  "checks for undocumented exported type members",
	Run:  run,
}

func run(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch t := n.(type) {
			case *ast.TypeSpec:
				if st, ok := t.Type.(*ast.StructType); ok {
					checkStructFields(pass, st, t.Name.Name)
				}
				if it, ok := t.Type.(*ast.InterfaceType); ok {
					checkInterfaceMethods(pass, it)
				}
			}
			return true
		})
	}
	return nil, nil
}

// validateDoc checks if the documentation is valid and starts with the field name
func validateDoc(doc *ast.CommentGroup, fieldName string) bool {
	if doc == nil {
		return false
	}

	for _, comment := range doc.List {
		text := comment.Text
		// Skip marker comments
		if strings.HasPrefix(strings.TrimSpace(text), "// +") {
			continue
		}
		// Check if the first actual comment starts with the field name
		if strings.HasPrefix(strings.TrimSpace(text), "// "+fieldName) {
			return true
		}
		// If we found a non-marker comment that doesn't start with the field name, return false
		return false
	}
	return false
}

func checkStructFields(pass *analysis.Pass, st *ast.StructType, typeName string) {
	for _, field := range st.Fields.List {
		// Skip if it's an embedded field (no field names) or if it's a common k8s field
		if len(field.Names) == 0 || isK8sCommonField(field.Names[0].Name, typeName) {
			continue
		}

		if field.Names[0].IsExported() {
			fieldName := field.Names[0].Name
			if !validateDoc(field.Doc, fieldName) {
				pass.Reportf(field.Pos(), "exported field %s must have documentation starting with '%s'", fieldName, fieldName)
			}
		}
	}
}

func checkInterfaceMethods(pass *analysis.Pass, it *ast.InterfaceType) {
	for _, method := range it.Methods.List {
		if len(method.Names) > 0 && method.Names[0].IsExported() {
			methodName := method.Names[0].Name
			if !validateDoc(method.Doc, methodName) {
				pass.Reportf(method.Pos(), "exported method %s must have documentation starting with '%s'", methodName, methodName)
			}
		}
	}
}

func isK8sCommonField(name, typeName string) bool {
	commonFields := map[string]bool{
		"Spec":   true,
		"Status": true,
	}

	// If the field is "Items" and the type ends with "List", skip it
	if name == "Items" && strings.HasSuffix(typeName, "List") {
		return true
	}

	return commonFields[name]
}

func main() {
	singlechecker.Main(Analyzer)
}

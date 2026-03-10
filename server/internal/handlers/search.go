package handlers

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type StructDefinition struct {
	Name string `json:"name"`
	File string `json:"file"`
}

type SearchStructsPayload struct {
	Status  string             `json:"status"`
	Structs []StructDefinition `json:"structs"`
	Code    int                `json:"code"`
}

func SearchStructs(w http.ResponseWriter, r *http.Request) {
	structs, err := findStructs(".")
	if err != nil {
		slog.Error("error searching for struct definitions", "error", err)
		http.Error(w, "500 - Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	payload := SearchStructsPayload{
		Status:  "OK",
		Structs: structs,
		Code:    http.StatusOK,
	}

	err = json.NewEncoder(w).Encode(payload)
	if err != nil {
		slog.Error("error encoding json", "error", err)
		http.Error(w, "500 - Internal Server Error", http.StatusInternalServerError)
	}
}

func findStructs(root string) ([]StructDefinition, error) {
	var structs []StructDefinition
	fset := token.NewFileSet()

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		node, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			slog.Warn("skipping file with parse error", "file", path, "error", parseErr)
			return nil
		}

		for _, decl := range node.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if _, ok := typeSpec.Type.(*ast.StructType); ok {
					structs = append(structs, StructDefinition{
						Name: typeSpec.Name.Name,
						File: path,
					})
				}
			}
		}
		return nil
	})

	if structs == nil {
		structs = []StructDefinition{}
	}
	return structs, err
}

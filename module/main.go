package main

import (
	"embed"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"golang.org/x/tools/go/ast/astutil"
)

//go:embed template/*
var templatesFS embed.FS

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Please provide a module name.")
		os.Exit(1)
	}
	moduleName := os.Args[1]

	modulePath, _ := getModulePath()
	projectName := filepath.Base(modulePath)

	if err := modifyAppGo(moduleName, projectName); err != nil {
		fmt.Printf("Error modifying app/app.go: %v\n", err)
		os.Exit(1)
	}

	tempDir, err := os.MkdirTemp("", "temp")
	if err != nil {
		fmt.Printf("Error creating temporary directory: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tempDir)

	if err := copyModule(moduleName, tempDir); err != nil {
		fmt.Printf("Error creating module: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Module created successfully! 🙌")
}

func modifyAppGo(moduleName, projectName string) error {
	appGoPath := "app/app.go"
	fset := token.NewFileSet()

	node, err := parser.ParseFile(fset, appGoPath, nil, parser.ParseComments)
	if err != nil {
		return err
	}

	// Add imports
	astutil.AddImport(fset, node, projectName+"/x/"+moduleName)
	astutil.AddNamedImport(fset, node, moduleName+"keeper ", projectName+"/x/"+moduleName+"/keeper")

	// Modify ModuleBasics
	astutil.Apply(node, nil, func(cursor *astutil.Cursor) bool {
		if callExpr, ok := cursor.Node().(*ast.CallExpr); ok {
			if selectorExpr, ok := callExpr.Fun.(*ast.SelectorExpr); ok {
				if selectorExpr.Sel.Name == "NewBasicManager" {
					newArg := ast.NewIdent(moduleName + ".AppModuleBasic{}")
					callExpr.Args = append(callExpr.Args, newArg)
				}
			}
		}
		return true
	})

	// Modify App struct
	astutil.Apply(node, nil, func(cursor *astutil.Cursor) bool {
		if typeSpec, ok := cursor.Node().(*ast.TypeSpec); ok {
			if typeSpec.Name.Name == "App" {
				structType, ok := typeSpec.Type.(*ast.StructType)
				if ok {
					newField := &ast.Field{
						Names: []*ast.Ident{ast.NewIdent(capitalize(moduleName) + "Keeper")},
						Type:  &ast.StarExpr{X: ast.NewIdent(moduleName + "keeper.Keeper")},
					}
					structType.Fields.List = append(structType.Fields.List, newField)
				}
			}
		}
		return true
	})

	// Modify depinject.Inject call
	astutil.Apply(node, nil, func(cursor *astutil.Cursor) bool {
		if callExpr, ok := cursor.Node().(*ast.CallExpr); ok {
			if ident, ok := callExpr.Fun.(*ast.Ident); ok {
				if ident.Name == "Inject" {
					newArg := &ast.UnaryExpr{
						Op: token.AND,
						X:  ast.NewIdent("app." + moduleName + "Keeper"),
					}
					callExpr.Args = append(callExpr.Args, newArg)
				}
			}
		}
		return true
	})

	// Write the changes to the file
	file, err := os.Create(appGoPath)
	if err != nil {
		return err
	}
	defer file.Close()
	err = format.Node(file, fset, node)
	if err != nil {
		return err
	}
	return nil
}

func copyModule(moduleName string, tempDir string) error {
	destDir := filepath.Join("x", moduleName)

	// Check if directory already exists
	if _, err := os.Stat(destDir); !os.IsNotExist(err) {
		return fmt.Errorf("directory '%s' already exists", destDir)
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	modulePath, err := getModulePath()
	if err != nil {
		return err
	}
	projectName := filepath.Base(modulePath)

	if err := fs.WalkDir(templatesFS, "template/module", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			subdir := strings.Replace(path, "template/module", filepath.Join(tempDir, "module", moduleName), 1)
			return os.MkdirAll(subdir, 0755)
		}
		return processFile(path, moduleName, projectName, filepath.Join(tempDir, "module"))
	}); err != nil {
		return err
	}

	protoTempDir := filepath.Join(tempDir, "proto", projectName, moduleName)
	return fs.WalkDir(templatesFS, "template/proto", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			subdir := strings.Replace(path, "template/proto", protoTempDir, 1)
			return os.MkdirAll(subdir, 0755)
		}
		return processFile(path, moduleName, projectName, filepath.Join(tempDir, "proto"))
	})
}

func processFile(path, moduleName, projectName, tempDir string) error {
	funcMap := template.FuncMap{"Capitalize": capitalize}
	tmpl, err := template.New(filepath.Base(path)).Funcs(funcMap).ParseFS(templatesFS, path)
	if err != nil {
		return err
	}

	modulePath, err := getModulePath()
	if err != nil {
		return err
	}
	data := struct {
		MODULE_PATH  string
		MODULE_NAME  string
		PROJECT_NAME string
	}{
		MODULE_PATH:  modulePath,
		MODULE_NAME:  moduleName,
		PROJECT_NAME: projectName,
	}

	filename := strings.Replace(path, ".tmpl", "", 1)
	destPath := ""
	if strings.Contains(path, "template/module") {
		destPath = strings.Replace(filename, "template/module", filepath.Join(tempDir, moduleName), 1)
	} else if strings.Contains(path, "template/proto") {
		destPath = strings.Replace(filename, "template/proto", filepath.Join(tempDir, projectName, moduleName), 1)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}

	destFile, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer destFile.Close()

	if err := tmpl.Execute(destFile, data); err != nil {
		return err
	}

	finalDestPath := ""
	if strings.Contains(path, "template/module") {
		finalDestPath = strings.Replace(filename, "template/module", filepath.Join("x", moduleName), 1)
	} else if strings.Contains(path, "template/proto") {
		finalDestPath = strings.Replace(filename, "template/proto", filepath.Join("proto", projectName, moduleName), 1)
	}

	if err := os.MkdirAll(filepath.Dir(finalDestPath), 0755); err != nil {
		return err
	}
	return os.Rename(destPath, finalDestPath)
}

func getModulePath() (string, error) {
	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(goMod), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.Replace(line, "module ", "", 1)), nil
		}
	}
	return "", fmt.Errorf("unable to determine module path from go.mod")
}

func capitalize(s string) string {
	if len(s) == 0 {
		return ""
	}
	return strings.ToUpper(string(s[0])) + s[1:]
}

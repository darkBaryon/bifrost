package host

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/framework/configstore/tables"
)

// 对账宿主实际赋值的非零/非空条件，避免上游合并语义改变后权限差异判定漂移。
func TestClientOptionalMatchesHostUpdateBranches(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	source, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(filename), "../../../../transports/bifrost-http/handlers/config.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	observed := map[string]bool{}
	typ := reflect.TypeOf(tables.TableClientConfig{})
	for _, decl := range source.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "updateConfig" {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			branch, ok := node.(*ast.IfStmt)
			if !ok {
				return true
			}
			expr, ok := branch.Cond.(*ast.BinaryExpr)
			if !ok {
				return true
			}
			field, ok := expr.X.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			client, ok := field.X.(*ast.SelectorExpr)
			if !ok || client.Sel.Name != "ClientConfig" {
				return true
			}
			payload, ok := client.X.(*ast.Ident)
			if !ok || payload.Name != "payload" {
				return true
			}
			empty := false
			switch rhs := expr.Y.(type) {
			case *ast.BasicLit:
				empty = rhs.Value == "0" || rhs.Value == `""`
			case *ast.Ident:
				empty = rhs.Name == "nil"
			}
			if !empty {
				return true
			}
			assigned := false
			ast.Inspect(branch.Body, func(n ast.Node) bool {
				a, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for _, lhs := range a.Lhs {
					f, ok := lhs.(*ast.SelectorExpr)
					if !ok || f.Sel.Name != field.Sel.Name {
						continue
					}
					owner, ok := f.X.(*ast.Ident)
					if ok && owner.Name == "updatedConfig" {
						assigned = true
					}
				}
				return true
			})
			if !assigned {
				return true
			}
			if expr.Op != token.GTR && expr.Op != token.NEQ {
				t.Errorf("update condition changed for %s: %s", field.Sel.Name, expr.Op)
			}
			f, ok := typ.FieldByName(field.Sel.Name)
			if !ok {
				t.Fatalf("unknown client field %s", field.Sel.Name)
			}
			observed[strings.Split(f.Tag.Get("json"), ",")[0]] = true
			return true
		})
	}
	if !reflect.DeepEqual(observed, clientOptional) {
		t.Fatalf("upstream optional fields %v; RBAC %v", observed, clientOptional)
	}
}

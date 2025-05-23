// Copyright 2018 The CUE Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cmd

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/tools/go/packages"

	cueast "cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/ast/astutil"
	"cuelang.org/go/cue/format"
	"cuelang.org/go/cue/literal"
	"cuelang.org/go/cue/load"
	"cuelang.org/go/cue/parser"
	cuetoken "cuelang.org/go/cue/token"
	"cuelang.org/go/internal"
)

// TODO:
// Document:
// - Use ast package.
// - how to deal with "oneOf" or sum types?
// - generate cue files for cue field tags?
// - cue go get or cue get go
// - include generation report in doc_gen.cue or report.txt.
//   Possible enums:
//   package foo
//   Type: enumType

func newGoCmd(c *Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "go [packages]",
		Short: "add Go dependencies to the current module",
		Long: `go converts Go types into CUE definitions

The command "cue get go" is like "go get", but converts the retrieved Go
packages to CUE. The retrieved packages are put in the CUE module's pkg
directory at the import path of the corresponding Go package. The converted
definitions are available to any CUE file within the CUE module by using
this import path.

The Go type definitions are converted to CUE based on how they would be
interpreted by Go's encoding/json package. Definitions for a Go file foo.go
are written to a CUE file named foo_go_gen.cue.

It is safe for users to add additional files to the generated directories,
as long as their name does not end with _gen.*.


Rules of Converting Go types to CUE

Go structs are converted to cue structs adhering to the following conventions:

	- field names are translated based on the definition of a "json" or "yaml"
	  tag, in that order.

	- embedded structs marked with a json inline tag unify with struct
	  definition. For instance, the Go struct

	    struct MyStruct {
			Common  ` + "json:\",inline\"" + `
			Field string
		 }

	  translates to the CUE struct

		 #MyStruct: Common & {
			 Field: string
		 }

	- a type that implements MarshalJSON, UnmarshalJSON, MarshalYAML, or
	  UnmarshalYAML is translated to top (_) to indicate it may be any
	  value. For some Go core types for which the implementation of these
	  methods is known, like time.Time, the type may be more specific.

	- a type implementing MarshalText or UnmarshalText is represented as
	  the CUE type string

	- slices and arrays convert to CUE lists, except when the element type is
	  byte, in which case it translates to the CUE bytes type.
	  In the case of arrays, the length of the CUE value is constrained
	  accordingly, when possible.

	- Maps translate to a CUE struct, where all elements are constrained to
	  be of Go map element type. Like for JSON, maps may only have string keys.

	- Pointers translate to a sum type with the default value of null and
	  the Go type as an alternative value.

	- Field tags are translated to CUE's field attributes. In some cases,
	  the contents are rewritten to reflect the corresponding types in CUE.
	  The @go attribute is added if the field name or type definition differs
	  between the generated CUE and the original Go.


Native CUE Constraints

Native CUE constraints may be defined in separate cue files alongside the
generated files either in the original Go directory or in the generated
directory. These files can impose additional constraints on types and values
that are not otherwise expressible in Go. The package name for these CUE files
must be the same as that of the Go package.

For instance, for the type

	package foo

	type IP4String string

defined in the Go package, one could add a cue file foo.cue with the following
contents to allow IP4String to assume only valid IP4 addresses:

	package foo

	// IP4String defines a valid IP4 address.
	#IP4String: =~#"^\#(byte)\.\#(byte)\.\#(byte)\.\#(byte)$"#

	// byte defines string allowing integer values of 0-255.
	byte = #"([01]?\d?\d|2[0-4]\d|25[0-5])"#


The "cue get go" command copies any cue files in the original Go package
directory that has a package clause with the same name as the Go package to the
destination directory, replacing its .cue ending with _gen.cue.

Alternatively, the additional native constraints can be added to the generated
package, as long as the file name does not end with _gen.cue.
Running cue get go again to regenerate the package will never overwrite any
files not ending with _gen.*.


Constants and Enums

Go does not have an enum or sum type. Conventionally, a type that is supposed
to be an enum is followed by a const block with the allowed values for that
type. However, as that is only a guideline and not a hard rule, these cases
cannot be translated to CUE disjunctions automatically.

Constant values, however, are generated in a way that makes it easy to convert
a type to a proper enum using native CUE constraints. For instance, the Go type

	package foo

	type Switch int

	const (
		Off Switch = iota
		On
	)

translates into the following CUE definitions:

	package foo

	#Switch: int // #enumSwitch

	#enumSwitch: Off | On

	Off: 0
	On:  1

This definition allows any integer value for #Switch, while the #enumSwitch
value defines all defined constants for Switch and thus all valid values if
#Switch were to be interpreted as an enum type. To turn #Switch into an enum,
include the following constraint in, say, enum.cue, in either the original
source directory or the generated directory:

	package foo

	// limit the valid values for Switch to those existing as constants with
	// the same type.
	#Switch: #enumSwitch

This tells CUE that only the values enumerated by #enumSwitch are valid values
for #Switch. Note that there are now two definitions of #Switch. CUE handles
this in the usual way by unifying the two definitions, in which case the more
restrictive enum interpretation of #Switch remains.
`,
		// - TODO: interpret cuego's struct tags and annotations.

		RunE: mkRunE(c, extract),
	}

	cmd.Flags().StringP(string(flagExclude), "e", "",
		"comma-separated list of regexps of identifiers to omit")

	cmd.Flags().Bool(string(flagLocal), false,
		"generates files in the main module locally")

	cmd.Flags().StringP(string(flagPackage), "p", "", "package name for generated CUE files")

	return cmd
}

const (
	flagExclude flagName = "exclude"
	flagLocal   flagName = "local"
)

func (e *extractor) initExclusions(str string) {
	e.exclude = str
	for _, re := range strings.Split(str, ",") {
		if re != "" {
			e.exclusions = append(e.exclusions, regexp.MustCompile(re))
		}
	}
}

func (e *extractor) filter(name string) bool {
	for _, ex := range e.exclusions {
		if ex.MatchString(name) {
			return true
		}
	}
	return false
}

type extractor struct {
	cmd *Command

	allPkgs map[string]*packages.Package
	done    map[string]bool

	// per package
	pkg      *packages.Package
	orig     map[types.Type]*ast.StructType
	usedPkgs map[string]bool
	consts   map[string][]string

	// per file
	cmap     ast.CommentMap
	pkgNames map[string]pkgInfo

	exclusions []*regexp.Regexp
	exclude    string
}

type pkgInfo struct {
	id   string
	name string
}

func (e *extractor) logf(format string, args ...interface{}) {
	if flagVerbose.Bool(e.cmd) {
		fmt.Fprintf(e.cmd.Stderr(), format+"\n", args...)
	}
}

func (e *extractor) usedPkg(pkg string) {
	e.usedPkgs[pkg] = true
}

var (
	typeAny    = types.Universe.Lookup("any").Type()    // any or interface{}
	typeByte   = types.Universe.Lookup("byte").Type()   // byte
	typeBytes  = types.NewSlice(typeByte)               // []byte
	typeString = types.Universe.Lookup("string").Type() // string
	typeError  = types.Universe.Lookup("error").Type()  // error
)

// Note that we can leave positions, packages, and parameter/result names empty.
// They are not used by go/types.Implements.

func typeMethod(name string, params, results []types.Type) *types.Func {
	return types.NewFunc(token.NoPos, nil, name, typeSignature(params, results))
}

func typeSignature(params, results []types.Type) *types.Signature {
	paramVars := make([]*types.Var, len(params))
	for i, param := range params {
		paramVars[i] = types.NewParam(token.NoPos, nil, "", param)
	}
	resultVars := make([]*types.Var, len(results))
	for i, result := range results {
		resultVars[i] = types.NewParam(token.NoPos, nil, "", result)
	}
	return types.NewSignatureType(
		nil,
		nil,
		nil,
		types.NewTuple(paramVars...),
		types.NewTuple(resultVars...),
		false,
	)
}

// Note that we record these interfaces without names, so they will show up in
// the logs like "interface{MarshalJSON() ([]uint8, error)}" rather than
// encoding/json.Marshaler. We could construct named types if need be.

var toTop = []*types.Interface{
	// json.Marshaler: interface { MarshalJSON() ([]byte, error) }
	types.NewInterfaceType([]*types.Func{
		typeMethod("MarshalJSON", nil, []types.Type{typeBytes, typeError}),
	}, nil).Complete(),

	// json.Unmarshaler: interface { UnmarshalJSON([]byte) error }
	types.NewInterfaceType([]*types.Func{
		typeMethod("UnmarshalJSON", []types.Type{typeBytes}, []types.Type{typeError}),
	}, nil).Complete(),

	// yaml.Marshaler: interface { MarshalYAML() (interface{}, error) }
	types.NewInterfaceType([]*types.Func{
		typeMethod("MarshalYAML", nil, []types.Type{typeAny, typeError}),
	}, nil).Complete(),

	// yaml.Unmarshaler: interface { UnmarshalYAML(func(interface{}) error) error }
	types.NewInterfaceType([]*types.Func{
		typeMethod("UnmarshalYAML", []types.Type{
			typeSignature([]types.Type{typeAny}, []types.Type{typeError}),
		}, []types.Type{typeError}),
	}, nil).Complete(),
}

var toString = []*types.Interface{
	// encoding.TextMarshaler: interface { MarshalText() ([]byte, error) }
	types.NewInterfaceType([]*types.Func{
		typeMethod("MarshalText", nil, []types.Type{typeBytes, typeError}),
	}, nil).Complete(),

	// encoding.TextUnmarshaler: interface { UnmarshalText([]byte) error }
	types.NewInterfaceType([]*types.Func{
		typeMethod("UnmarshalText", []types.Type{typeBytes}, []types.Type{typeError}),
	}, nil).Complete(),
}

// TODO:
// - consider not including types with any dropped fields.

func extract(cmd *Command, args []string) error {
	// TODO the CUE load using "." (below) assumes that a CUE module and a Go
	// module will exist within the same directory (more precisely a Go module
	// could be nested within a CUE module), such that the module path in any
	// subdirectory below the current directory will be the same.  This seems an
	// entirely reasonable restriction, but also one that we should enforce.
	//
	// Enforcing this restriction also makes --local entirely redundant.

	// command specifies a Go package(s) that belong to the main module
	// and where for some reason the
	// determine module root:
	binst := loadFromArgs([]string{"."}, nil)[0]

	// TODO: require explicitly set root.
	root := binst.Root

	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule,
	}
	pkgs, err := packages.Load(cfg, args...)
	if err != nil {
		return err
	}
	var errs []string
	for _, p := range pkgs {
		for _, e := range p.Errors {
			switch e.Kind {
			case packages.ParseError, packages.TypeError:
				// TODO: Consider making this behavior an option.
			default:
				errs = append(errs, fmt.Sprintf("\t%s: %v", p.PkgPath, e))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("could not load Go packages:\n%s", strings.Join(errs, "\n"))
	}

	e := extractor{
		cmd:     cmd,
		allPkgs: map[string]*packages.Package{},
		orig:    map[types.Type]*ast.StructType{},
	}

	e.initExclusions(flagExclude.String(cmd))

	e.done = map[string]bool{}

	for _, p := range pkgs {
		e.done[p.PkgPath] = true
		e.addPackage(p)
	}

	for _, p := range pkgs {
		if err := e.extractPkg(root, p); err != nil {
			return err
		}
	}
	return nil
}

func (e *extractor) addPackage(p *packages.Package) {
	if pkg, ok := e.allPkgs[p.PkgPath]; ok {
		if p != pkg {
			panic(fmt.Sprintf("duplicate package %s", p.PkgPath))
		}
		return
	}
	e.allPkgs[p.PkgPath] = p
	for _, pkg := range p.Imports {
		e.addPackage(pkg)
	}
}

func (e *extractor) recordTypeInfo(p *packages.Package) {
	for _, f := range p.Syntax {
		for n := range ast.Preorder(f) {
			switch n := n.(type) {
			case *ast.StructType:
				e.orig[p.TypesInfo.TypeOf(n)] = n
			}
		}
	}
}

func (e *extractor) extractPkg(root string, p *packages.Package) error {
	e.pkg = p
	e.logf("--- Package %s", p.PkgPath)

	e.recordTypeInfo(p)

	e.consts = map[string][]string{}

	for _, f := range p.Syntax {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				e.recordConsts(d)
			}
		}
	}

	pkg := p.PkgPath
	dir := filepath.Join(load.GenPath(root), filepath.FromSlash(pkg))

	isMain := flagLocal.Bool(e.cmd) && p.Module != nil && p.Module.Main
	if isMain {
		dir = p.Module.Dir
		sub := p.PkgPath[len(p.Module.Path):]
		if sub != "" {
			dir = filepath.FromSlash(dir + sub)
		}
	}

	if err := os.MkdirAll(dir, 0777); err != nil {
		return err
	}

	e.usedPkgs = map[string]bool{}

	args := pkg
	if e.exclude != "" {
		args += " --exclude=" + e.exclude
	}

	for i, f := range p.Syntax {
		e.cmap = ast.NewCommentMap(p.Fset, f, f.Comments)

		e.pkgNames = map[string]pkgInfo{}

		for _, spec := range f.Imports {
			pkgPath, _ := strconv.Unquote(spec.Path.Value)
			pkg := p.Imports[pkgPath]

			info := pkgInfo{id: pkgPath, name: pkg.Name}
			if path.Base(pkgPath) != pkg.Name {
				info.id += ":" + pkg.Name
			}

			if spec.Name != nil {
				info.name = spec.Name.Name
			}

			e.pkgNames[pkgPath] = info
		}

		decls := []cueast.Decl{}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.GenDecl:
				decls = append(decls, e.reportDecl(d)...)
			}
		}

		if len(decls) == 0 && f.Doc == nil {
			continue
		}

		pName := flagPackage.String(e.cmd)
		if pName == "" {
			pName = p.Name
		}

		pkg := &cueast.Package{Name: e.ident(pName, false)}
		addDoc(f.Doc, pkg)

		f := &cueast.File{Decls: []cueast.Decl{
			&cueast.CommentGroup{List: []*cueast.Comment{
				{Text: "// Code generated by cue get go. DO NOT EDIT."},
			}},
			&cueast.CommentGroup{List: []*cueast.Comment{
				{Text: "//cue:generate cue get go " + args},
			}},
			pkg,
		}}
		f.Decls = append(f.Decls, decls...)

		if err := astutil.Sanitize(f); err != nil {
			return err
		}

		file := filepath.Base(p.CompiledGoFiles[i])

		file = strings.Replace(file, ".go", "_go", 1)
		file += "_gen.cue"
		b, err := format.Node(f, format.Simplify())
		if err != nil {
			return err
		}
		err = os.WriteFile(filepath.Join(dir, file), b, 0666)
		if err != nil {
			return err
		}
	}

	if !isMain {
		if err := e.importCUEFiles(p, dir, args); err != nil {
			return err
		}
	}

	for path := range e.usedPkgs {
		if !e.done[path] {
			e.done[path] = true
			if err := e.extractPkg(root, e.allPkgs[path]); err != nil {
				return err
			}
		}
	}

	return nil
}

func (e *extractor) importCUEFiles(p *packages.Package, dstDir, args string) error {
	// A package's Go files will often share directories,
	// for example all original source files will sit in the same directory.
	// Deduplicate the directories so we don't repeat work.
	var srcDirs []string
	for _, path := range p.CompiledGoFiles {
		srcDirs = append(srcDirs, filepath.Dir(path))
	}
	slices.Sort(srcDirs)
	srcDirs = slices.Compact(srcDirs)

	for _, srcDir := range srcDirs {
		entries, err := os.ReadDir(srcDir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			path := filepath.Join(srcDir, name)
			name, ok := strings.CutSuffix(name, ".cue")
			if !ok {
				continue
			}
			f, err := parser.ParseFile(path, nil, parser.PackageClauseOnly)
			if err != nil {
				return err
			}

			if pkg := f.PackageName(); pkg != "" && pkg == p.Name {
				w := &bytes.Buffer{}
				fmt.Fprintf(w, "// Code generated by cue get go. DO NOT EDIT.\n\n")
				fmt.Fprintf(w, "//cue:generate cue get go %v\n\n", args)
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				w.Write(b)
				dst := filepath.Join(dstDir, name+"_gen.cue")
				if err := os.WriteFile(dst, w.Bytes(), 0666); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (e *extractor) recordConsts(x *ast.GenDecl) {
	if x.Tok != token.CONST {
		return
	}
	for _, s := range x.Specs {
		v, ok := s.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, n := range v.Names {
			typ := e.pkg.TypesInfo.TypeOf(n).String()
			e.consts[typ] = append(e.consts[typ], n.Name)
		}
	}
}

func (e *extractor) strLabel(name string) cueast.Label {
	return cueast.NewString(name)
}

func (e *extractor) ident(name string, isDef bool) *cueast.Ident {
	if isDef {
		r := []rune(name)[0]
		name = "#" + name
		if !unicode.Is(unicode.Lu, r) {
			name = "_" + name
		}
	}
	return cueast.NewIdent(name)
}

func (e *extractor) def(doc *ast.CommentGroup, name string, value cueast.Expr, newline bool) *cueast.Field {
	f := &cueast.Field{
		Label: e.ident(name, true), // Go identifiers are always valid CUE identifiers.
		Value: value,
	}
	addDoc(doc, f)
	if newline {
		cueast.SetRelPos(f, cuetoken.NewSection)
	}
	return f
}

func (e *extractor) reportDecl(x *ast.GenDecl) (a []cueast.Decl) {
	switch x.Tok {
	case token.TYPE:
		for _, s := range x.Specs {
			v, ok := s.(*ast.TypeSpec)
			if !ok || e.filter(v.Name.Name) {
				continue
			}

			typ := e.pkg.TypesInfo.TypeOf(v.Name)
			enums := e.consts[typ.String()]
			name := v.Name.Name
			mapNamed := false
			underlying := e.pkg.TypesInfo.TypeOf(v.Type)
			if b, ok := underlying.Underlying().(*types.Basic); ok && b.Kind() != types.String {
				switch b.Kind() {
				case types.Invalid:
					continue
				case types.String:
				default:
					mapNamed = true
				}
			}

			switch tn, ok := e.pkg.TypesInfo.Defs[v.Name].(*types.TypeName); {
			case ok:
				if altType := e.altType(tn.Type()); altType != nil {
					// TODO: add the underlying tag as a Go tag once we have
					// proper string escaping for CUE.
					a = append(a, e.def(x.Doc, name, altType, true))
					break
				}
				fallthrough

			default:
				if !supportedType(nil, typ) {
					e.logf("    Dropped declaration %v of unsupported type %v", name, typ)
					continue
				}
				if s := e.altType(typ); s != nil {
					a = append(a, e.def(x.Doc, name, s, true))
					break
				}

				f, _ := e.makeField(name, definition, underlying, x.Doc, true)
				a = append(a, f)
				cueast.SetRelPos(f, cuetoken.NewSection)

			}

			if len(enums) > 0 && ast.IsExported(name) {
				enumName := "#enum" + name
				cueast.AddComment(a[len(a)-1], internal.NewComment(false, enumName))

				// Constants are mapped as definitions.
				var exprs []cueast.Expr
				var named []cueast.Decl
				for _, v := range enums {
					if v == "_" {
						continue
					}
					label := cueast.NewString(v)
					cueast.SetRelPos(label, cuetoken.Blank)

					var x cueast.Expr = e.ident(v, true)
					cueast.SetRelPos(x, cuetoken.Newline)
					exprs = append(exprs, x)

					if !mapNamed {
						continue
					}

					named = append(named, &cueast.Field{
						Label: label,
						Value: e.ident(v, true),
					})
				}

				addField := func(label string, exprs []cueast.Expr) {
					f := &cueast.Field{
						Label: cueast.NewIdent(label),
						Value: cueast.NewBinExpr(cuetoken.OR, exprs...),
					}
					cueast.SetRelPos(f, cuetoken.NewSection)
					a = append(a, f)
				}

				addField(enumName, exprs)
				if len(named) > 0 {
					f := &cueast.Field{
						Label: cueast.NewIdent("#values_" + name),
						Value: &cueast.StructLit{Elts: named},
					}
					cueast.SetRelPos(f, cuetoken.NewSection)
					a = append(a, f)
				}
			}
		}

	case token.CONST:
		// TODO: copy over comments for constant blocks.

		for k, s := range x.Specs {
			// TODO: determine type name and filter.
			v, ok := s.(*ast.ValueSpec)
			if !ok {
				continue
			}

			for i, name := range v.Names {
				if name.Name == "_" || e.filter(name.Name) {
					continue
				}
				f := e.def(v.Doc, name.Name, nil, k == 0)
				a = append(a, f)

				val := ""
				if i < len(v.Values) {
					if lit, ok := v.Values[i].(*ast.BasicLit); ok {
						val = lit.Value
					}
				}

				typ := e.pkg.TypesInfo.TypeOf(name)
				v := e.pkg.TypesInfo.Defs[v.Names[i]].(*types.Const).Val()
				var cv cueast.Expr
				switch v.Kind() {
				case constant.String:
					s := constant.StringVal(v)
					bl := &cueast.BasicLit{Kind: cuetoken.STRING}
					// Go strings may contain any bytes, even invalid UTF-8.
					// CUE strings may only contain valid UTF-8, because it has
					// bytes values for anything that may not be valid UTF-8.
					if utf8.ValidString(s) {
						bl.Value = literal.String.Quote(s)
					} else {
						bl.Value = literal.Bytes.Quote(s)
					}
					cv = bl

				default:
					// TODO(mvdan): replace this with switch cases for Bool/Int/Float
					sv := v.ExactString()
					var err error
					cv, err = parser.ParseExpr("", sv)
					if err != nil {
						panic(fmt.Errorf("failed to parse %v: %v", sv, err))
					}
				}

				// Use the original Go value if compatible with CUE (octal is okay)
				if b, ok := cv.(*cueast.BasicLit); ok {
					if b.Kind == cuetoken.INT && val != "" && val[0] != '\'' {
						b.Value = val
					}
					if b.Value != val {
						cueast.AddComment(cv, internal.NewComment(false, val))
					}
				}

				switch typ {
				case typeByte, typeString, typeError:
				default:
					if basic, ok := typ.(*types.Basic); ok && basic.Info()&types.IsUntyped != 0 {
						break // untyped basic types do not make valid identifiers
					}
					cv = cueast.NewBinExpr(cuetoken.AND, e.makeType(typ), cv)
				}

				f.Value = cv
			}
		}
	}
	return a
}

func shortTypeName(t types.Type) string {
	if n, ok := t.(*types.Named); ok {
		return n.Obj().String() // fully qualified, e.g. "foo/bar.Baz"
	}
	return t.String() // anonymous, e.g. "interface{Method() []byte}"
}

func (e *extractor) altType(typ types.Type) cueast.Expr {
	// We need to check whether T or *T implement each interface I.
	// Typically we would just need to check whether *T implements I,
	// as the method set of *T includes the method set of T,
	// but that doesn't work when T is an interface type.
	// See https://go.dev/ref/spec#Method_sets.
	//
	// TODO(mvdan): perhaps check with T when it's an interface,
	// and with *T otherwise, avoiding double calls to types.Implements.
	ptr := types.NewPointer(typ)
	for _, iface := range toTop {
		if types.Implements(typ, iface) || types.Implements(ptr, iface) {
			t := shortTypeName(typ)
			e.logf("    %v implements %s; setting type to _", t, iface)
			return e.ident("_", false)
		}
	}
	for _, iface := range toString {
		if types.Implements(typ, iface) || types.Implements(ptr, iface) {
			t := shortTypeName(typ)
			e.logf("    %v implements %s; setting type to string", t, iface)
			return e.ident("string", false)
		}
	}
	return nil
}

func addDoc(g *ast.CommentGroup, x cueast.Node) bool {
	doc := makeDoc(g, true)
	if doc != nil {
		cueast.AddComment(x, doc)
		return true
	}
	return false
}

func makeDoc(g *ast.CommentGroup, isDoc bool) *cueast.CommentGroup {
	if g == nil {
		return nil
	}

	a := []*cueast.Comment{}

	for _, comment := range g.List {
		c := comment.Text

		// Remove comment markers.
		// The parser has given us exactly the comment text.
		switch c[1] {
		case '/':
			//-style comment (no newline at the end)
			a = append(a, &cueast.Comment{Text: c})

		case '*':
			/*-style comment */
			c = c[2 : len(c)-2]
			if len(c) > 0 && c[0] == '\n' {
				c = c[1:]
			}

			lines := strings.Split(c, "\n")

			// Find common space prefix
			i := 0
			line := lines[0]
			for ; i < len(line); i++ {
				if c := line[i]; c != ' ' && c != '\t' {
					break
				}
			}

			for _, l := range lines {
				for j := 0; j < i && j < len(l); j++ {
					if line[j] != l[j] {
						i = j
						break
					}
				}
			}

			// Strip last line if empty.
			if n := len(lines); n > 1 && len(lines[n-1]) < i {
				lines = lines[:n-1]
			}

			// Print lines.
			for _, l := range lines {
				if i >= len(l) {
					a = append(a, &cueast.Comment{Text: "//"})
					continue
				}
				a = append(a, &cueast.Comment{Text: "// " + l[i:]})
			}
		}
	}
	return &cueast.CommentGroup{Doc: isDoc, List: a}
}

func supportedType(stack []types.Type, t types.Type) (ok bool) {
	// handle recursive types
	for _, t0 := range stack {
		if t0 == t {
			return true
		}
	}
	stack = append(stack, t)

	if named, ok := t.(*types.Named); ok {
		obj := named.Obj()

		// Redirect or drop Go standard library types.
		if obj.Pkg() == nil {
			// error interface
			return true
		}
		switch obj.Pkg().Path() {
		case "time":
			switch named.Obj().Name() {
			case "Time", "Duration", "Location", "Month", "Weekday":
				return true
			}
			return false
		case "math/big":
			switch named.Obj().Name() {
			case "Int", "Float":
				return true
			}
			// case "net":
			// 	// TODO: IP, Host, SRV, etc.
			// case "url":
			// 	// TODO: URL and Values
		}
	}

	// Note that underlying types are never aliases.
	t = t.Underlying()
	switch t := t.(type) {
	case *types.Basic:
		return t.Kind() != types.Invalid
	case *types.Named:
		return true
	case *types.Pointer:
		return supportedType(stack, t.Elem())
	case *types.Slice:
		return supportedType(stack, t.Elem())
	case *types.Array:
		return supportedType(stack, t.Elem())
	case *types.Map:
		if b, ok := t.Key().Underlying().(*types.Basic); !ok || b.Kind() != types.String {
			return false
		}
		return supportedType(stack, t.Elem())
	case *types.Struct:
		// Eliminate structs with fields for which all fields are filtered.
		if t.NumFields() == 0 {
			return true
		}
		for i := 0; i < t.NumFields(); i++ {
			f := t.Field(i)
			if f.Exported() && supportedType(stack, f.Type()) {
				return true
			}
		}
	case *types.Interface:
		return true
	}
	return false
}

type fieldKind int

const (
	regular fieldKind = iota
	optional
	definition
)

func (e *extractor) makeField(name string, kind fieldKind, expr types.Type, doc *ast.CommentGroup, newline bool) (f *cueast.Field, typename string) {
	typ := e.makeType(expr)
	var label cueast.Label
	if kind == definition {
		label = e.ident(name, true)
	} else {
		label = e.strLabel(name)
	}
	f = &cueast.Field{Label: label, Value: typ}
	if doc := makeDoc(doc, newline); doc != nil {
		f.AddComment(doc)
		cueast.SetRelPos(doc, cuetoken.NewSection)
	}

	var tok cuetoken.Token
	switch kind {
	case definition:
		// neither optional nor required.
	case regular:
		// TODO(required): use idiomatic CUE (token.NOT) if CUE version is
		// higher than a certain number? We may not be able to determine this
		// accurately enough.
	case optional:
		tok = cuetoken.OPTION
	}

	internal.SetConstraint(f, tok)

	b, _ := format.Node(typ)
	return f, string(b)
}

func (e *extractor) makeType(typ types.Type) (result cueast.Expr) {
	switch typ := types.Unalias(typ).(type) {
	case *types.Named:
		obj := typ.Obj()
		pkg := obj.Pkg()
		if pkg == nil {
			return e.ident("_", false)
		}
		// Check for builtin packages.
		switch {
		case pkg.Path() == "time" && obj.Name() == "Time":
			ref := e.ident(e.pkgNames[pkg.Path()].name, false)
			var name *cueast.Ident
			if ref.Name != "time" {
				name = e.ident(ref.Name, false)
			}
			ref.Node = cueast.NewImport(name, "time")
			return cueast.NewSel(ref, obj.Name())

		case pkg.Path() == "time" && obj.Name() == "Duration":
			// Go's time.Duration is an int64 to represent nanoseconds,
			// and even though most Go users would find the string representation
			// like "3s" or "40m" most reasonable and readable for constant values,
			// encoding/json is bound via Go's compatibility promise to encoding as integers.
			//
			// Since compatibility with JSON encoding and decoding in Go
			// is more important than readability, we use integers as well here.
			// Note that this means we aren't compatible with CUE's own time.Duration,
			// which is rather unfortunate, but we have to choose one or the other.
			//
			// TODO(mvdan): reconsider once 'cue get go' is more configurable,
			// and especially once encoding/json/v2 becomes a reality,
			// as it does encode time.Duration via strings rather than integers.
			// For example, could we generate types like 'int | *time.Duration'
			// and constants like '300 | *"300ns"' to support both at the same time?
			return e.ident("int", false)

		case pkg.Path() == "math/big" && obj.Name() == "Int":
			return e.ident("int", false)

		default:
			// Any Go standard library type that hasn't been handled above is not supported;
			// fall back to whatever alternative type we can, or just "top".
			// Otherwise we would end up generating "std" packages which clash with our
			// own standard library, for example cue.mod/gen/time.
			//
			// Go defines standard library as "no dot in the first path element",
			// such that "foo/bar.baz" is still technically part of the standard library.
			// Note that "example" and "test" are reserved for users in Go, such that "test/foo"
			// is not part of Go's standard library, but such exceptions do not exist in CUE.
			// TODO: never place such packages under e.g. cue.mod/gen/example, as that clashes
			// with CUE's own standard library namespace at the moment.
			//
			// TODO: for cases where the Go std type could be supported, we could still generate
			// and import it under a non-std CUE package, such as cue.mod/gen/pkg.go.dev/time.
			// TODO: Doc?
			firstElem, _, _ := strings.Cut(pkg.Path(), "/")
			if !strings.ContainsAny(firstElem, ".") {
				if s := e.altType(obj.Type()); s != nil {
					return s
				}
				return e.ident("_", false)
			}
		}

		result = e.ident(obj.Name(), true)
		if pkg != e.pkg.Types {
			info := e.pkgNames[pkg.Path()]
			if info.name == "" {
				info.name = pkg.Name()
			}
			p := e.ident(info.name, false)
			var name *cueast.Ident
			if info.name != pkg.Name() {
				name = e.ident(info.name, false)
			}
			if info.id == "" {
				// This may happen if an alias is defined in a different file
				// within this package referring to yet another package.
				info.id = pkg.Path()
			}
			p.Node = cueast.NewImport(name, info.id)
			// makeType is always called to describe a type, so whatever
			// this is referring to, it must be a definition.
			result = cueast.NewSel(p, "#"+obj.Name())
			e.usedPkg(pkg.Path())
		}

		// TODO(uhthomas): Fields with type parameters should not be
		// top.
		//
		// For example:
		//
		// 	type A[T any] struct {
		// 		SomeField T
		// 	}
		//
		// 	type B A[string]
		//
		// Should become:
		//
		// 	#A: SomeField: _
		//
		// 	#B: #A & {
		// 		_#T: string
		// 		SomeField: _#T
		// 	}
		//
		// Or maybe:
		//
		// 	#A: {
		// 		#T: _
		// 		SomeField: #T
		// 	}
		//
		// 	#B: #A & {
		// 		#T: string
		// 	}
		//
		// The values of x.TypeParams() and x.TypeArgs() may be helpful.

		// params := x.TypeParams()
		// args := x.TypeArgs()
		// if params.Len() > 0 {
		// 	var fields []any
		// 	for i := 0; i < params.Len(); i++ {
		// 		name := params.At(i).Obj().Name()
		// 		fields = append(fields, e.ident(name, true), e.makeType(args.At(i)))
		// 	}
		// 	return cueast.NewBinExpr(cuetoken.AND, result, cueast.NewStruct(fields...))
		// }

		return result
	case *types.Pointer:
		return &cueast.BinaryExpr{
			X:  cueast.NewNull(),
			Op: cuetoken.OR,
			Y:  e.makeType(typ.Elem()),
		}

	case *types.Struct:
		st := &cueast.StructLit{
			Lbrace: cuetoken.Blank.Pos(),
			Rbrace: cuetoken.Newline.Pos(),
		}
		e.addFields(typ, st)
		return st

	case *types.Slice:
		// Note that []byte is treated different from []uint8,
		// even though byte is an alias for the basic type uint8.
		// TODO: reconsider this; both encoding/json and the future v2
		// encode []uint8, or anything assignable to []byte, as bytes.
		if typ.Elem() == typeByte {
			return e.ident("bytes", false)
		}
		return cueast.NewList(&cueast.Ellipsis{Type: e.makeType(typ.Elem())})

	case *types.Array:
		if typ.Elem() == typeByte {
			// TODO: no way to constrain lengths of bytes for now, as regexps
			// operate on Unicode, not bytes. So we need
			//     fmt.Fprint(e.w, fmt.Sprintf("=~ '^\C{%d}$'", x.Len())),
			// but regexp does not support that.
			// But translate to bytes, instead of [...byte] to be consistent.
			return e.ident("bytes", false)
		} else {
			return &cueast.BinaryExpr{
				X: &cueast.BasicLit{
					Kind:  cuetoken.INT,
					Value: strconv.Itoa(int(typ.Len())),
				},
				Op: cuetoken.MUL,
				Y:  cueast.NewList(e.makeType(typ.Elem())),
			}
		}

	case *types.Map:
		if b, ok := typ.Key().Underlying().(*types.Basic); !ok || b.Kind() != types.String {
			panic(fmt.Sprintf("unsupported map key type %T", typ.Key()))
		}

		f := &cueast.Field{
			Label: cueast.NewList(e.ident("string", false)),
			Value: e.makeType(typ.Elem()),
		}
		cueast.SetRelPos(f, cuetoken.Blank)
		return &cueast.StructLit{
			Lbrace: cuetoken.Blank.Pos(),
			Elts:   []cueast.Decl{f},
			Rbrace: cuetoken.Blank.Pos(),
		}

	case *types.Basic:
		switch typ.Kind() {
		case types.Uintptr, types.UnsafePointer:
			return e.ident("uint64", false)
		case types.Byte:
			return e.ident("uint8", false)
		case types.Complex64, types.Complex128:
			return e.ident("_", false)
		}
		return e.ident(typ.Name(), false)

	case *types.Union:
		var exprs []cueast.Expr
		for i := 0; i < typ.Len(); i++ {
			exprs = append(exprs, e.makeType(typ.Term(i).Type()))
		}
		return cueast.NewBinExpr(cuetoken.OR, exprs...)

	case *types.Interface:
		// TODO(uhthomas): Should interfaces with methods (IsMethodSet)
		// be set to top?
		if !typ.IsComparable() {
			return e.ident("_", false)
		}

		// TODO(uhthomas): Simplify expressions.
		//
		// For example:
		//
		// 	int | (int | string)
		//
		// Should really become:
		//
		// 	int | string
		//
		var exprs []cueast.Expr
		for i := 0; i < typ.NumEmbeddeds(); i++ {
			exprs = append(exprs, e.makeType(typ.EmbeddedType(i)))
		}
		return cueast.NewBinExpr(cuetoken.OR, exprs...)

	case *types.TypeParam:
		return e.makeType(typ.Constraint())

	default:
		// record error
		panic(fmt.Sprintf("unsupported type %T", typ))
	}
}

func (e *extractor) addAttr(f *cueast.Field, tag, body string) {
	s := fmt.Sprintf("@%s(%s)", tag, body)
	f.Attrs = append(f.Attrs, &cueast.Attribute{Text: s})
}

func (e *extractor) addFields(x *types.Struct, st *cueast.StructLit) {
	add := func(x cueast.Decl) {
		st.Elts = append(st.Elts, x)
	}

	s := e.orig[x]
	docs := []*ast.CommentGroup{}
	for _, f := range s.Fields.List {
		if len(f.Names) == 0 {
			docs = append(docs, f.Doc)
		} else {
			for range f.Names {
				docs = append(docs, f.Doc)
			}
		}
	}
	count := 0
	for i := 0; i < x.NumFields(); i++ {
		f := x.Field(i)
		if !ast.IsExported(f.Name()) {
			continue
		}
		if !supportedType(nil, f.Type()) {
			e.logf("    Dropped field %v for unsupported type %v", f.Name(), f.Type())
			continue
		}
		if f.Anonymous() && e.isInline(x.Tag(i)) {
			typ := f.Type()
			for {
				p, ok := typ.(*types.Pointer)
				if !ok {
					break
				}
				typ = p.Elem()
			}
			switch typ := types.Unalias(typ).(type) {
			case *types.Named:
				embed := &cueast.EmbedDecl{Expr: e.makeType(typ)}
				if i > 0 {
					cueast.SetRelPos(embed, cuetoken.NewSection)
				}
				add(embed)
			case *types.Struct:
				e.addFields(typ, st)
			default:
				panic(fmt.Sprintf("unimplemented embedding for type %T", x))
			}
			continue
		}
		tag := x.Tag(i)
		name := getName(f.Name(), tag)
		if name == "-" {
			continue
		}

		doc := docs[i]

		// TODO: check referrers
		kind := regular
		if e.isOptional(f, doc, tag) {
			kind = optional
		}
		field, cueType := e.makeField(name, kind, f.Type(), doc, count > 0)
		add(field)

		if s := reflect.StructTag(tag).Get("cue"); s != "" {
			expr, err := parser.ParseExpr("get go", s)
			if err != nil {
				e.logf("error parsing struct tag %q:", s, err)
			}
			field.Value = cueast.NewBinExpr(cuetoken.AND, field.Value, expr)
		}

		// Add field tag to convert back to Go.
		typeName := f.Type().String()
		// simplify type names:
		for path, info := range e.pkgNames {
			typeName = strings.ReplaceAll(typeName, path+".", info.name+".")
		}
		typeName = strings.ReplaceAll(typeName, e.pkg.Types.Path()+".", "")

		cueStr := strings.ReplaceAll(cueType, "_#", "")
		cueStr = strings.ReplaceAll(cueStr, "#", "")

		// TODO: remove fields in @go attr that are the same as printed?
		if name != f.Name() || typeName != cueStr {
			buf := &strings.Builder{}
			if name != f.Name() {
				buf.WriteString(f.Name())
			}

			if typeName != cueStr {
				if strings.ContainsAny(typeName, `#"',()=`) {
					typeName = literal.String.Quote(typeName)
				}
				fmt.Fprint(buf, ",", typeName)
			}
			e.addAttr(field, "go", buf.String())
		}

		// Carry over protobuf field tags with modifications.
		// TODO: consider trashing the protobuf tag, as the Go versions are
		// lossy and will not allow for an accurate translation in some cases.
		tags := reflect.StructTag(tag)
		if t := tags.Get("protobuf"); t != "" {
			split := strings.Split(t, ",")
			k := 0
			for _, s := range split {
				if strings.HasPrefix(s, "name=") && s[len("name="):] == name {
					continue
				}
				split[k] = s
				k++
			}
			split = split[:k]

			// Put tag first, as type could potentially be elided and is
			// "more optional".
			if len(split) >= 2 {
				split[0], split[1] = split[1], split[0]
			}

			// Interpret as map?
			if len(split) > 2 && split[1] == "bytes" {
				tk := tags.Get("protobuf_key")
				tv := tags.Get("protobuf_val")
				if tk != "" && tv != "" {
					tk, _, _ = strings.Cut(tk, ",")
					tv, _, _ = strings.Cut(tv, ",")
					split[1] = fmt.Sprintf("map[%s]%s", tk, tv)
				}
			}

			e.addAttr(field, "protobuf", strings.Join(split, ","))
		}

		// Carry over XML tags.
		if t := reflect.StructTag(tag).Get("xml"); t != "" {
			e.addAttr(field, "xml", t)
		}

		// Carry over TOML tags.
		if t := reflect.StructTag(tag).Get("toml"); t != "" {
			e.addAttr(field, "toml", t)
		}

		// TODO: should we in general carry over any unknown tag verbatim?

		count++
	}
}

func (e *extractor) isInline(tag string) bool {
	return hasFlag(tag, "json", "inline", 1) ||
		hasFlag(tag, "yaml", "inline", 1)
}

func (e *extractor) isOptional(f *types.Var, doc *ast.CommentGroup, tag string) bool {
	if _, ok := f.Type().(*types.Pointer); ok {
		return true
	}

	for _, line := range strings.Split(doc.Text(), "\n") {
		before, _, _ := strings.Cut(strings.TrimSpace(line), "=")
		if before == "+optional" {
			return true
		}
	}

	// Go 1.24 added the "omitzero" option to encoding/json, an improvement over "omitempty".
	// Note that, as of mid 2025, YAML libraries don't seem to have picked up "omitzero" yet.
	// TODO: also when the type is a list or other kind of pointer.
	return hasFlag(tag, "json", "omitempty", 1) ||
		hasFlag(tag, "json", "omitzero", 1) ||
		hasFlag(tag, "yaml", "omitempty", 1)
}

func hasFlag(tag, key, flag string, offset int) bool {
	if t := reflect.StructTag(tag).Get(key); t != "" {
		split := strings.Split(t, ",")
		if offset >= len(split) {
			return false
		}
		for _, str := range split[offset:] {
			if str == flag {
				return true
			}
		}
	}
	return false
}

func getName(name string, tag string) string {
	tags := reflect.StructTag(tag)
	for _, s := range []string{"json", "yaml"} {
		if tag, ok := tags.Lookup(s); ok {
			tag, _, _ = strings.Cut(tag, ",")
			if tag != "" {
				return tag
			}
		}
	}
	// TODO: should we also consider to protobuf name? Probably not.

	return name
}

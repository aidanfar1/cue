// Copyright 2019 CUE Authors
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

package openapi

import (
	"fmt"
	"strings"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
	cuejson "cuelang.org/go/encoding/json"
	internaljson "cuelang.org/go/internal/encoding/json"
)

// A Config defines options for converting CUE to and from OpenAPI.
type Config struct {
	// PkgName defines to package name for a generated CUE package.
	PkgName string

	// Info specifies the info section of the OpenAPI document. To be a valid
	// OpenAPI document, it must include at least the title and version fields.
	// Info may be a *ast.StructLit or any type that marshals to JSON.
	Info interface{}

	// NameFunc allows users to specify an alternative representation
	// for references. It is called with the value passed to the top level
	// method or function and the path to the entity being generated.
	// If it returns an empty string the generator will  expand the type
	// in place and, if applicable, not generate a schema for that entity.
	//
	// Note: this only returns the final element of the /-separated
	// reference.
	NameFunc func(val cue.Value, path cue.Path) string

	// DescriptionFunc allows rewriting a description associated with a certain
	// field. A typical implementation compiles the description from the
	// comments obtains from the Doc method. No description field is added if
	// the empty string is returned.
	DescriptionFunc func(v cue.Value) string

	// SelfContained causes all non-expanded external references to be included
	// in this document.
	SelfContained bool

	// OpenAPI version to use. Supported as of v3.0.0.
	Version string

	// FieldFilter defines a regular expression of all fields to omit from the
	// output. It is only allowed to filter fields that add additional
	// constraints. Fields that indicate basic types cannot be removed. It is
	// an error for such fields to be excluded by this filter.
	// Fields are qualified by their Object type. For instance, the
	// minimum field of the schema object is qualified as Schema/minimum.
	FieldFilter string

	// ExpandReferences replaces references with actual objects when generating
	// OpenAPI Schema. It is an error for an CUE value to refer to itself
	// if this option is used.
	ExpandReferences bool

	// StrictFeatures reports an error for features that are known
	// to be unsupported.
	StrictFeatures bool

	// StrictKeywords reports an error when unknown keywords
	// are encountered. For OpenAPI 3.0, this is implicitly always
	// true, as that specification explicitly prohibits unknown keywords
	// other than "x-" prefixed keywords.
	StrictKeywords bool
}

type Generator = Config

// Gen generates the set OpenAPI schema for all top-level types of the
// given instance.
func Gen(inst cue.InstanceOrValue, c *Config) ([]byte, error) {
	if c == nil {
		c = defaultConfig
	}
	all, err := schemas(c, inst)
	if err != nil {
		return nil, err
	}
	top, err := c.compose(inst, all)
	if err != nil {
		return nil, err
	}
	topValue := inst.Value().Context().BuildExpr(top)
	if err := topValue.Err(); err != nil {
		return nil, err
	}
	return internaljson.Marshal(topValue)
}

// Generate generates the set of OpenAPI schema for all top-level types of the
// given instance.
//
// Note: only a limited number of top-level types are supported so far.
func Generate(inst cue.InstanceOrValue, c *Config) (*ast.File, error) {
	if c == nil {
		c = defaultConfig
	}
	all, err := schemas(c, inst)
	if err != nil {
		return nil, err
	}
	top, err := c.compose(inst, all)
	if err != nil {
		return nil, err
	}
	return &ast.File{Decls: top.Elts}, nil
}

func toCUE(name string, x interface{}) (v ast.Expr, err error) {
	b, err := internaljson.Marshal(x)
	if err == nil {
		v, err = cuejson.Extract(name, b)
	}
	if err != nil {
		return nil, errors.Wrapf(err, token.NoPos,
			"openapi: could not encode %s", name)
	}
	return v, nil

}

func (c *Config) compose(inst cue.InstanceOrValue, schemas *ast.StructLit) (x *ast.StructLit, err error) {
	val := inst.Value()
	var errs errors.Error

	var title, version string
	var info *ast.StructLit

	for i, _ := val.Fields(); i.Next(); {
		label := i.Selector().Unquoted()
		attr := i.Value().Attribute("openapi")
		if s, _ := attr.String(0); s != "" {
			label = s
		}
		switch label {
		case "$version":
		case "-":
		case "info":
			info, _ = i.Value().Syntax().(*ast.StructLit)
			if info == nil {
				errs = errors.Append(errs, errors.Newf(i.Value().Pos(),
					"info must be a struct"))
			}
			title, _ = i.Value().LookupPath(cue.MakePath(cue.Str("title"))).String()
			version, _ = i.Value().LookupPath(cue.MakePath(cue.Str("version"))).String()

		default:
			errs = errors.Append(errs, errors.Newf(i.Value().Pos(),
				"openapi: unsupported top-level field %q", label))
		}
	}

	switch x := c.Info.(type) {
	case nil:
		if title == "" {
			title = "Generated by cue."
			for _, d := range val.Doc() {
				title = strings.TrimSpace(d.Text())
				break
			}
		}

		if version == "" {
			version, _ = val.LookupPath(cue.MakePath(cue.Str("$version"))).String()
			if version == "" {
				version = "no version"
			}
		}

		if info == nil {
			info = ast.NewStruct(
				"title", ast.NewString(title),
				"version", ast.NewString(version),
			)
		} else {
			m := (*orderedMap)(info)
			m.setExpr("title", ast.NewString(title))
			m.setExpr("version", ast.NewString(version))
		}

	case *ast.StructLit:
		info = x
	default:
		x, err := toCUE("info section", x)
		if err != nil {
			return nil, err
		}
		var ok bool
		info, ok = x.(*ast.StructLit)
		if !ok {
			errs = errors.Append(errs, errors.Newf(token.NoPos,
				"Info field supplied must marshal to a struct but got %s", fmt.Sprintf("%T", x)))
		}
	}

	return ast.NewStruct(
		"openapi", ast.NewString(c.Version),
		"info", info,
		"paths", ast.NewStruct(),
		"components", ast.NewStruct("schemas", schemas),
	), errs
}

var defaultConfig = &Config{}

// TODO
// The conversion interprets @openapi(<entry> {, <entry>}) attributes as follows:
//
//      readOnly        sets the readOnly flag for a property in the schema
//                      only one of readOnly and writeOnly may be set.
//      writeOnly       sets the writeOnly flag for a property in the schema
//                      only one of readOnly and writeOnly may be set.
//      discriminator   explicitly sets a field as the discriminator field
//

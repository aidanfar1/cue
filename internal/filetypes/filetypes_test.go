// Copyright 2020 CUE Authors
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

package filetypes

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/errors"
)

func check(t *testing.T, want, x interface{}, err error) {
	t.Helper()
	if err != nil {
		x = errors.String(err.(errors.Error))
	}
	if diff := cmp.Diff(want, x, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("unexpected result; -want +got\n%s", diff)
	}
}

func TestFromFile(t *testing.T) {
	testCases := []struct {
		name string
		in   build.File
		mode Mode
		out  interface{}
	}{{
		name: "must specify encoding",
		in:   build.File{},
		out:  `no encoding specified`,
	}, {
		// Default without any
		name: "cue",
		in: build.File{
			Filename: "",
			Encoding: build.CUE,
		},
		mode: Def,
		out: &FileInfo{
			Encoding:     build.CUE,
			Form:         build.Schema,
			Definitions:  true,
			Data:         true,
			Optional:     true,
			Constraints:  true,
			References:   true,
			Cycles:       true,
			KeepDefaults: true,
			Incomplete:   true,
			Imports:      true,
			Docs:         true,
			Attributes:   true,
		},
	}, {
		// CUE encoding in data form.
		name: "data: cue",
		in: build.File{
			Filename: "",
			Form:     build.Data,
			Encoding: build.CUE,
		},
		mode: Input,
		out: &FileInfo{
			Filename:   "",
			Encoding:   build.CUE,
			Form:       build.Data,
			Data:       true,
			Docs:       true,
			Attributes: true,
		},
	}, {
		// Filename starting with a . but no other extension.
		name: "filename-with-dot",
		in: build.File{
			Filename: ".json",
		},
		mode: Def,
		out:  `no encoding specified`,
	}, {
		name: "yaml",
		mode: Def,
		in: build.File{
			Filename: "foo.yaml",
			Encoding: build.YAML,
		},
		out: &FileInfo{
			Filename:   "foo.yaml",
			Encoding:   build.YAML,
			Form:       build.Graph,
			Data:       true,
			References: true,
			Cycles:     true,
			Stream:     true,
			Docs:       true,
			Attributes: true,
		},
	}, {
		name: "yaml+openapi",
		in: build.File{
			Filename:       "foo.yaml",
			Encoding:       build.YAML,
			Interpretation: build.OpenAPI,
		},
		out: &FileInfo{
			Filename:       "foo.yaml",
			Encoding:       build.YAML,
			Interpretation: build.OpenAPI,
			Form:           build.Schema,
			Definitions:    true,
			Data:           true,
			Optional:       true,
			Constraints:    true,
			References:     true,
			Cycles:         true,
			KeepDefaults:   true,
			Incomplete:     true,
			Imports:        true,
			Docs:           true,
			Attributes:     true,
		},
	}, {
		name: "JSONDefault",
		mode: Input,
		in: build.File{
			Filename: "data.json",
			Encoding: build.JSON,
		},
		out: &FileInfo{
			Filename:     "data.json",
			Encoding:     build.JSON,
			Form:         build.Data,
			Definitions:  false,
			Data:         true,
			Optional:     false,
			Constraints:  false,
			References:   false,
			Cycles:       false,
			KeepDefaults: false,
			Incomplete:   false,
			Imports:      false,
			Docs:         false,
			Attributes:   false,
		},
	}, {
		name: "JSONSchema",
		in: build.File{
			Filename:       "foo.json",
			Interpretation: "jsonschema",
			Encoding:       build.JSON,
		},
		out: &FileInfo{
			Filename:       "foo.json",
			Encoding:       build.JSON,
			Interpretation: "jsonschema",
			Form:           build.Schema,
			Definitions:    true,
			Data:           true,
			Optional:       true,
			Constraints:    true,
			References:     true,
			Cycles:         true,
			KeepDefaults:   true,
			Incomplete:     true,
			Imports:        true,
			Docs:           true,
			Attributes:     true,
		},
	}, {
		name: "JSONOpenAPI",
		in: build.File{
			Filename:       "foo.json",
			Encoding:       build.JSON,
			Interpretation: build.OpenAPI,
		},
		mode: Def,
		out: &FileInfo{
			Filename:       "foo.json",
			Encoding:       build.JSON,
			Interpretation: build.OpenAPI,
			Form:           build.Schema,
			Definitions:    true,
			Data:           true,
			Optional:       true,
			Constraints:    true,
			References:     true,
			Cycles:         true,
			KeepDefaults:   true,
			Incomplete:     true,
			Imports:        true,
			Docs:           true,
			Attributes:     true,
		},
	}, {
		name: "KoalaXML",
		in: build.File{
			Filename: "foo.xml",
			Encoding: "xml",
			BoolTags: map[string]bool{
				"koala": true,
			},
		},
		mode: Def,
		out: &FileInfo{
			Filename:     "foo.xml",
			Encoding:     "xml",
			Form:         "data",
			Definitions:  false,
			Data:         true,
			Optional:     false,
			Constraints:  false,
			References:   false,
			Cycles:       false,
			KeepDefaults: false,
			Incomplete:   false,
			Imports:      false,
			Docs:         true,
			Attributes:   true,
		},
	}, {
		name: "OpenAPIDefaults",
		in: build.File{
			Filename:       "-",
			Encoding:       build.JSON,
			Interpretation: build.OpenAPI,
		},
		mode: Def,
		out: &FileInfo{
			Filename:       "-",
			Encoding:       build.JSON,
			Interpretation: build.OpenAPI,
			Form:           build.Schema,
			Definitions:    true,
			Data:           true,
			Optional:       true,
			Constraints:    true,
			References:     true,
			Cycles:         true,
			KeepDefaults:   true,
			Incomplete:     true,
			Imports:        true,
			Docs:           true,
			Attributes:     true,
		},
	}, {
		name: "Go",
		in: build.File{
			Encoding: build.Code,
			Filename: "foo.go",
		},
		mode: Def,
		out: &FileInfo{
			Filename:     "foo.go",
			Encoding:     build.Code,
			Form:         build.Schema,
			Definitions:  true,
			Data:         true,
			Optional:     true,
			Constraints:  true,
			References:   true,
			Cycles:       true,
			KeepDefaults: true,
			Incomplete:   true,
			Imports:      true,
			Docs:         true,
			Attributes:   true,
		},
	}}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := FromFile(&tc.in, tc.mode)
			check(t, tc.out, info, err)
		})
	}
}

func TestParseFile(t *testing.T) {
	// TODO(errors): wrong path?
	testCases := []struct {
		in   string
		mode Mode
		out  interface{}
	}{{
		in:   "file.json",
		mode: Input,
		out: &build.File{
			Filename:       "file.json",
			Encoding:       build.JSON,
			Interpretation: build.Auto,
		},
	}, {
		in:   ".json",
		mode: Input,
		out:  `no encoding specified for file ".json"`,
	}, {
		in:   ".json.yaml",
		mode: Input,
		out: &build.File{
			Filename:       ".json.yaml",
			Encoding:       build.YAML,
			Interpretation: build.Auto,
		},
	}, {
		in:   "file.json",
		mode: Def,
		out: &build.File{
			Filename: "file.json",
			Encoding: build.JSON,
		},
	}, {
		in: "schema:file.json",
		out: &build.File{
			Filename:       "file.json",
			Encoding:       build.JSON,
			Interpretation: build.Auto,
			Form:           build.Schema,
		},
	}, {
		in: "openapi:-",
		out: &build.File{
			Filename:       "-",
			Encoding:       build.JSON,
			Interpretation: build.OpenAPI,
			Form:           build.Schema,
			BoolTags: map[string]bool{
				"strict":         false,
				"strictFeatures": false,
				"strictKeywords": false,
			},
		},
	}, {
		in: "cue:file.json",
		out: &build.File{
			Filename: "file.json",
			Encoding: build.CUE,
		},
	}, {
		in: "cue+schema:-",
		out: &build.File{
			Filename: "-",
			Encoding: build.CUE,
			Form:     build.Schema,
		},
	}, {
		in: "code+lang=js:foo.x",
		out: &build.File{
			Filename: "foo.x",
			Encoding: build.Code,
			Tags:     map[string]string{"lang": "js"},
		},
	}, {
		in:  "json+lang=js:foo.x",
		out: `tag lang is not allowed in this context`,
	}, {
		in:  "foo:file.bar",
		out: `unknown filetype foo`,
	}, {
		in:  "file.bar",
		out: `unknown file extension .bar`,
	}, {
		in:   "-",
		mode: Input,
		out: &build.File{
			Filename: "-",
			Encoding: build.CUE,
		},
	}, {
		in:   "-",
		mode: Export,
		out: &build.File{
			Filename: "-",
			Encoding: build.JSON,
		},
	}}
	for _, tc := range testCases {
		t.Run(tc.in, func(t *testing.T) {
			f, err := ParseFile(tc.in, tc.mode)
			check(t, tc.out, f, err)
		})
	}
}

func TestParseArgs(t *testing.T) {
	testCases := []struct {
		in  string
		out interface{}
	}{{
		in: "foo.json baz.yaml",
		out: []*build.File{
			{
				Filename:       "foo.json",
				Encoding:       build.JSON,
				Interpretation: build.Auto,
			},
			{
				Filename:       "baz.yaml",
				Encoding:       build.YAML,
				Interpretation: build.Auto,
			},
		},
	}, {
		in: "data: foo.cue",
		out: []*build.File{
			{Filename: "foo.cue", Encoding: build.CUE, Form: build.Data},
		},
	}, {
		in: "json: foo.json bar.data jsonschema: bar.schema",
		out: []*build.File{
			{Filename: "foo.json", Encoding: build.JSON}, // no auto!
			{Filename: "bar.data", Encoding: build.JSON},
			{
				Filename:       "bar.schema",
				Encoding:       build.JSON,
				Form:           build.Schema,
				Interpretation: "jsonschema",
				BoolTags: map[string]bool{
					"strict":         false,
					"strictFeatures": false,
					"strictKeywords": false,
				},
			},
		},
	}, {
		in: "koala: bar.xml",
		out: []*build.File{
			{
				Filename:       "bar.xml",
				Encoding:       "xml",
				Interpretation: "auto",
				BoolTags: map[string]bool{
					"koala": true,
				},
			},
		},
	}, {
		in: "jsonschema+strict: bar.schema",
		out: []*build.File{
			{
				Filename:       "bar.schema",
				Encoding:       "json",
				Interpretation: "jsonschema",
				Form:           build.Schema,
				BoolTags: map[string]bool{
					"strict":         true,
					"strictFeatures": true,
					"strictKeywords": true,
				},
			},
		},
	}, {
		in: "jsonschema+strictFeatures=1: bar.schema",
		out: []*build.File{
			{
				Filename:       "bar.schema",
				Encoding:       "json",
				Interpretation: "jsonschema",
				Form:           build.Schema,
				BoolTags: map[string]bool{
					"strict":         false,
					"strictFeatures": true,
					"strictKeywords": false,
				},
			},
		},
	}, {
		in: `json: c:\foo.json c:\path\to\file.dat`,
		out: []*build.File{
			{Filename: `c:\foo.json`, Encoding: build.JSON},
			{Filename: `c:\path\to\file.dat`, Encoding: build.JSON},
		},
	}, {
		in:  "json: json+schema: bar.schema",
		out: `scoped qualifier "json:" without file`,
	}, {
		in:  "json:",
		out: `scoped qualifier "json:" without file`,
	}, {
		in:  "json:foo:bar.yaml",
		out: `unsupported file name "json:foo:bar.yaml": may not have ':'`,
	}}
	for _, tc := range testCases {
		t.Run(tc.in, func(t *testing.T) {
			files, err := ParseArgs(strings.Split(tc.in, " "))
			check(t, tc.out, files, err)
		})
	}
}

func TestDefaultTagsForInterpretation(t *testing.T) {
	tags := DefaultTagsForInterpretation(build.JSONSchema, Input)
	qt.Assert(t, qt.DeepEquals(tags, map[string]bool{
		"strict":         false,
		"strictFeatures": false,
		"strictKeywords": false,
	}))
}

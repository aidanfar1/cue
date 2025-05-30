// Copyright 2021 CUE Authors
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

package cue

import (
	"cuelang.org/go/internal/core/adt"
)

// This file contains query-related code.

// getScopePrefix finds the Vertex that exists in v for the longest prefix of p.
//
// It is used to make the parent scopes visible when resolving expressions.
func getScopePrefix(v Value, p Path) Value {
	for _, sel := range p.Selectors() {
		w := v.LookupPath(MakePath(sel))
		if !w.Exists() {
			break
		}
		v = w
	}
	return v
}

// LookupPath reports the value for path p relative to v.
//
// Use [AnyString] and [AnyIndex] to find the value of undefined element types
// for structs and lists respectively, for example for the patterns in
// `{[string]: int}` and `[...string]`.
func (v Value) LookupPath(p Path) Value {
	if v.v == nil {
		return Value{}
	}
	n := v.v
	parent := v.parent_
	ctx := v.ctx()

outer:
	for _, sel := range p.path {
		f := sel.sel.feature(v.idx)
		deref := n.DerefValue()
		for _, a := range deref.Arcs {
			if a.Label == f {
				if a.IsConstraint() && !sel.sel.isConstraint() {
					break
				}
				parent = linkParent(parent, n, a)
				n = a
				continue outer
			}
		}
		if sel.sel.isConstraint() {
			x := &adt.Vertex{
				Parent: n,
				Label:  sel.sel.feature(ctx),
			}
			deref.MatchAndInsert(ctx, x)
			if x.HasConjuncts() {
				x.Finalize(ctx)
				parent = linkParent(parent, n, x)
				n = x
				continue
			}
		}

		var x *adt.Bottom
		if err, ok := sel.sel.(pathError); ok {
			x = &adt.Bottom{Err: err.Error}
		} else {
			x = mkErr(n, adt.EvalError, "field not found: %v", sel.sel)
			if n.Accept(ctx, f) {
				x.Code = adt.IncompleteError
			}
			x.NotExists = true
		}
		v := makeValue(v.idx, n, parent)
		return newErrValue(v, x)
	}
	return makeValue(v.idx, n, parent)
}

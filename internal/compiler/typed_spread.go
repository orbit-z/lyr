package compiler

// The typed pass of the spreads on component tags (PLAN.md §2.11): pass 1
// emits each as a SiteSpread call (SpreadCheck, RestFromAny, RestMergeAny)
// whose second argument is the callee's zero Props and whose last is the
// operand. An attribute type on a callee with a lyr.RestAttrs field is
// merged into it (WriteSpreadAttrs); a struct, or a pointer to one, sets
// props field by field (WriteSpreadProps); anything else is an error. Then
// each tag's order rules: a value that is always overwritten is an error.

import (
	"fmt"
	"go/ast"
	"go/types"
	"slices"
	"strings"
)

// tySpreadCall is one SiteSpread call of pass 1.
type tySpreadCall struct {
	site *CallSite
	call *ast.CallExpr
}

// tyField is one field of a struct's field set: encoding/json's (exported
// fields, promoted through embedded structs, a shallower field shadowing a
// deeper one, two at the same depth dropping each other; tags ignored).
type tyField struct {
	name string
	typ  types.Type
	// ptr is the embedded pointer type the field is promoted through, nil
	// when there's none.
	ptr types.Type
}

// structFields returns st's field set, by depth then declaration order.
func structFields(st *types.Struct) []tyField {
	type entry struct {
		st  *types.Struct
		ptr types.Type
	}
	next := []entry{{st, nil}}
	visited := map[*types.Struct]bool{}
	seen := map[string]bool{} // names at a shallower depth
	var out []tyField
	for len(next) > 0 {
		current := next
		next = nil
		count := map[string]int{}
		var level []tyField
		for _, en := range current {
			if visited[en.st] {
				continue
			}
			visited[en.st] = true
			for i := range en.st.NumFields() {
				f := en.st.Field(i)
				if f.Embedded() {
					ft, ptr := f.Type(), en.ptr
					if p, ok := types.Unalias(ft).(*types.Pointer); ok {
						ft = p.Elem()
						if ptr == nil {
							ptr = f.Type()
						}
					}
					if s, ok := ft.Underlying().(*types.Struct); ok {
						next = append(next, entry{s, ptr})
						continue
					}
				}
				if !f.Exported() {
					continue
				}
				level = append(level, tyField{name: f.Name(), typ: f.Type(), ptr: en.ptr})
				count[f.Name()]++
			}
		}
		for _, f := range level {
			if !seen[f.name] && count[f.name] == 1 {
				out = append(out, f)
			}
		}
		for name := range count {
			seen[name] = true
		}
	}
	return out
}

// tySpread is a classified props spread: its write (nil after an error),
// the fields it sets, and whether its type is the callee's Props.
type tySpread struct {
	sw        *SpreadWrite
	fields    []string
	identical bool
}

// spreadWrites classifies the spread calls of a unit into writes and
// returns their errors, then each tag's order errors.
func (e *tyEnv) spreadWrites(calls []tySpreadCall, info *types.Info, writes TypedWrites) []Diagnostic {
	var diags []Diagnostic
	spreads := map[int]*tySpread{}
	var tags []*SpreadTag
	for _, sc := range calls {
		if sc.site.Spread != nil && !slices.Contains(tags, sc.site.Spread) {
			tags = append(tags, sc.site.Spread)
		}
		w, sp, ds := e.spreadWrite(sc, info)
		diags = append(diags, ds...)
		if w.Kind != WriteFallback {
			writes[sc.site.Key] = w
		}
		if sp != nil {
			spreads[sc.site.Key] = sp
		}
	}
	for _, tag := range tags {
		diags = append(diags, spreadOrder(tag, spreads)...)
		if len(tag.Items) > 0 && tag.Items[0].Field == "" {
			if sp := spreads[tag.Items[0].Attr.Code.Start]; sp != nil && sp.identical && sp.sw != nil {
				sp.sw.Copy = true
			}
		}
	}
	return diags
}

// spreadWrite classifies one spread call.
func (e *tyEnv) spreadWrite(sc tySpreadCall, info *types.Info) (TypedWrite, *tySpread, []Diagnostic) {
	site, call := sc.site, sc.call
	if len(call.Args) < 5 || site.Spread == nil || site.Attr == nil {
		return TypedWrite{}, nil, nil
	}
	ptv, ok := info.Types[call.Args[1]]
	if !ok || ptv.Type == nil {
		return TypedWrite{}, nil, nil
	}
	props := ptv.Type
	pst, ok := props.Underlying().(*types.Struct)
	if !ok {
		return TypedWrite{}, nil, nil
	}
	tv, ok := info.Types[call.Args[len(call.Args)-1]]
	if !ok || tv.Type == nil {
		return TypedWrite{}, nil, nil
	}
	tag := site.Spread
	q := func(p *types.Package) string { return p.Name() }
	op := strings.TrimSpace(site.Code.Text)
	what := spreadText(site.Attr) + " on <" + tag.Tag + ">"
	errAt := func(format string, args ...any) []Diagnostic {
		return []Diagnostic{{SeverityError, site.Attr.Span, fmt.Sprintf(format, args...)}}
	}
	t := types.Unalias(tv.Type)
	if identical(t, e.attrs) || identical(t, e.attrList) || identical(t, e.restAttrs) {
		if tag.Rest != "" {
			return TypedWrite{Kind: WriteSpreadAttrs}, nil, nil
		}
		return TypedWrite{}, nil, errAt("{...} spread on <%s> needs a lyr.RestAttrs field in its Props, to pass HTML attributes through", tag.Tag)
	}
	base, ptr := t, false
	if p, ok := t.Underlying().(*types.Pointer); ok && !isUntyped(t) {
		base, ptr = p.Elem(), true
	}
	sst, ok := base.Underlying().(*types.Struct)
	if !ok || isUntyped(t) {
		got := "untyped nil"
		if !isUntyped(t) {
			got = types.TypeString(tv.Type, q)
		}
		return TypedWrite{}, nil, errAt("%s: %s is %s; spread a struct (its fields set props) or a lyr.Attrs, lyr.AttrList or lyr.RestAttrs (pass-through attributes)", what, op, got)
	}
	dst := structFields(pst)
	byName := make(map[string]tyField, len(dst))
	for _, f := range dst {
		byName[f.name] = f
	}
	sw := &SpreadWrite{Ptr: ptr}
	sp := &tySpread{identical: types.Identical(types.Unalias(base), types.Unalias(props))}
	var ds []Diagnostic
	var extra []string
	for _, f := range structFields(sst) {
		if f.ptr != nil {
			ds = append(ds, errAt("%s can't read %s: it's promoted through the embedded pointer %s; pass it as an attribute", spreadText(site.Attr), f.name, types.TypeString(f.ptr, q))...)
			continue
		}
		d, ok := byName[f.name]
		if !ok {
			extra = append(extra, f.name)
			continue
		}
		if d.ptr != nil {
			ds = append(ds, errAt("%s can't set %s of <%s>: it's promoted through the embedded pointer %s", spreadText(site.Attr), f.name, tag.Tag, types.TypeString(d.ptr, q))...)
			continue
		}
		switch elem, isOpt := optElem(d.typ); {
		case types.AssignableTo(f.typ, d.typ):
			sw.Fields = append(sw.Fields, SpreadField{Name: f.name})
		case isOpt && types.AssignableTo(f.typ, elem):
			sw.Fields = append(sw.Fields, SpreadField{Name: f.name, Opt: true})
		default:
			ds = append(ds, errAt("%s sets %s of <%s>: %s.%s is %s, the prop is %s", spreadText(site.Attr), f.name, tag.Tag,
				op, f.name, types.TypeString(f.typ, q), types.TypeString(d.typ, q))...)
			continue
		}
		sp.fields = append(sp.fields, f.name)
	}
	if len(extra) > 0 {
		names := make([]string, len(dst))
		for i, f := range dst {
			names[i] = LowerCamel(f.name)
		}
		list := tag.Tag + " takes no props"
		if len(names) > 0 {
			list = "props: " + strings.Join(names, ", ")
		}
		ds = append(ds, errAt("%s: %s has fields that aren't props of %s (%s); spread a struct whose fields are all props, or pass them one by one (%s)",
			what, types.TypeString(base, q), tag.Tag, strings.Join(extra, ", "), list)...)
	}
	if len(ds) > 0 {
		return TypedWrite{}, nil, ds
	}
	sp.sw = sw
	return TypedWrite{Kind: WriteSpreadProps, Spread: sw}, sp, nil
}

// spreadOrder checks the order rules of one tag (PLAN.md §2.11 (Q3)): an
// attribute before a spread that sets the same field, and two spreads
// that set the same field, are errors.
func spreadOrder(tag *SpreadTag, spreads map[int]*tySpread) []Diagnostic {
	var ds []Diagnostic
	sets := func(i int) []string {
		it := tag.Items[i]
		if it.Field != "" {
			return []string{it.Field}
		}
		if sp := spreads[it.Attr.Code.Start]; sp != nil && sp.sw != nil {
			return sp.fields
		}
		return nil
	}
	for i, it := range tag.Items {
		if it.Field != "" {
			for j := i + 1; j < len(tag.Items); j++ {
				if tag.Items[j].Field == "" && slices.Contains(sets(j), it.Field) {
					ds = append(ds, Diagnostic{SeverityError, it.Attr.Span, fmt.Sprintf("%s is overwritten by %s after it (it sets %s); move it after the spread to override",
						attrShort(it.Attr), spreadText(tag.Items[j].Attr), it.Field)})
					break
				}
			}
			continue
		}
		mine := sets(i)
		for j := range i {
			if tag.Items[j].Field != "" {
				continue
			}
			var both []string
			for _, f := range sets(j) {
				if slices.Contains(mine, f) {
					both = append(both, f)
				}
			}
			if len(both) > 0 {
				ds = append(ds, Diagnostic{SeverityError, it.Attr.Span, fmt.Sprintf("%s and %s both set %s of <%s>; a spread can't override another",
					spreadText(tag.Items[j].Attr), spreadText(it.Attr), strings.Join(both, ", "), tag.Tag)})
				break
			}
		}
	}
	return ds
}

// attrShort is an attribute as the order errors show it: title="…",
// title={…}, {title}, class:list={…} or a bare name.
func attrShort(a *Attr) string {
	switch {
	case a.Kind == AttrShorthand:
		return "{" + a.Name + "}"
	case a.IsExpr():
		return a.Name + "={…}"
	case a.IsStatic():
		return a.Name + `="…"`
	}
	return a.Name
}

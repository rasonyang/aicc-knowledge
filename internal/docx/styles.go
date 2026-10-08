// SPDX-License-Identifier: Apache-2.0

package docx

import "strconv"

type styleInfo struct {
	basedOn string
	outline int // -1 when the style does not set outlineLvl
	bad     string
}

type styleSet struct {
	byID        map[string]*styleInfo
	defaultPara string
}

func loadStyles(root *node) *styleSet {
	s := &styleSet{byID: map[string]*styleInfo{}}
	if root == nil {
		return s
	}
	for _, st := range root.kids {
		if st.name != "style" {
			continue
		}
		id, _ := st.attr("styleId")
		if id == "" {
			continue
		}
		if t, _ := st.attr("type"); t != "paragraph" {
			continue
		}
		si := &styleInfo{outline: -1}
		si.basedOn, _ = st.child("basedOn").attr("val")
		if ol := st.child("pPr").child("outlineLvl"); ol != nil {
			v, _ := ol.attr("val")
			if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 9 {
				si.outline = n
			} else {
				si.bad = v
			}
		}
		s.byID[id] = si
		if d, _ := st.attr("default"); d == "1" || d == "true" {
			s.defaultPara = id
		}
	}
	return s
}

// outline resolves the outline level of a paragraph style, following basedOn.
// It returns -1 when no style in the chain sets one.
func (s *styleSet) outline(id string, b *builder) int {
	if id == "" {
		id = s.defaultPara
	}
	if id == "" {
		return -1
	}
	seen := map[string]bool{}
	cur := id
	for cur != "" {
		if seen[cur] {
			b.warn(WarnStyleCycle, "basedOn cycle through style "+cur+" starting at "+id, "styles.xml#style:"+id)
			return -1
		}
		seen[cur] = true
		si, ok := s.byID[cur]
		if !ok {
			if cur == id {
				b.warn(WarnStyleNotFound, "style "+cur+" is not defined", "styles.xml#style:"+cur)
			} else {
				b.warn(WarnStyleBasedOnMissing, "style "+cur+" (basedOn ancestor of "+id+") is not defined", "styles.xml#style:"+id)
			}
			return -1
		}
		if si.bad != "" {
			b.warn(WarnInvalidOutlineLvl, "style outlineLvl "+strconv.Quote(si.bad)+" is not 0..9", "styles.xml#style:"+cur)
		}
		if si.outline >= 0 {
			return si.outline
		}
		cur = si.basedOn
	}
	return -1
}

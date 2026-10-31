package compiler

import "sort"

// goOffset implements GoOffset. A segment that contains off strictly wins
// over one that merely ends at it, so with adjacent segments the cursor maps
// into the one starting at off.
func (m *SourceMap) goOffset(off int) (int, bool) {
	if m == nil {
		return 0, false
	}
	best := -1
	for i, s := range m.Segments {
		if s.Kind == SegEcho || s.Kind == SegSpread || off < s.LyrStart || off > s.LyrEnd {
			continue
		}
		if off < s.LyrEnd {
			best = i
			break
		}
		if best < 0 {
			best = i
		}
	}
	if best < 0 {
		return 0, false
	}
	s := m.Segments[best]
	return s.GoStart + min(off-s.LyrStart, s.GoEnd-s.GoStart), true
}

// lyrOffset implements LyrOffset, by binary search over the sorted Go
// ranges.
func (m *SourceMap) lyrOffset(goOff int) (int, bool) {
	if m == nil {
		return 0, false
	}
	segs := m.Segments
	i := sort.Search(len(segs), func(i int) bool { return segs[i].GoEnd >= goOff })
	for ; i < len(segs) && segs[i].GoStart <= goOff; i++ {
		s := segs[i]
		if goOff == s.GoEnd && i+1 < len(segs) && segs[i+1].GoStart == goOff {
			continue // the next segment starts here: prefer it
		}
		if s.Kind == SegEcho {
			return 0, false
		}
		return s.LyrStart + min(goOff-s.GoStart, s.LyrEnd-s.LyrStart), true
	}
	return 0, false
}

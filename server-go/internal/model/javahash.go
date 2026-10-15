package model

import "unicode/utf16"

// HashSet reproduces the iteration order of a java.util.HashSet<String> (a HashMap).
// Java sends several collections in that order (supported commands, notificators,
// statistics per protocol), and the order is deterministic: String.hashCode is fixed by
// the language spec. Clients see it, and stored JSON keeps it.
//
// The model: a power-of-two table of buckets, the spread hash h ^ (h >>> 16) masked to
// the table size, entries in insertion order within a bucket, and doubling when the size
// passes 0.75 × capacity (doubling keeps each bucket's relative order, which equals
// re-inserting in iteration order). Buckets of 8+ entries become trees in Java; with the
// small sets used here that doesn't happen, and Add panics rather than silently diverge.
type HashSet struct {
	buckets [][]string
	size    int
}

// NewHashSet is new HashSet<>(): capacity 16.
func NewHashSet() *HashSet { return &HashSet{buckets: make([][]string, 16)} }

// NewHashSetOf is new HashSet<>(collection): capacity for size/0.75 + 1 (at least 16),
// then the items added in the given order (the source collection's iteration order).
func NewHashSetOf(items []string) *HashSet {
	want := max(int(float32(len(items))/0.75)+1, 16)
	capacity := 1
	for capacity < want {
		capacity <<= 1
	}
	s := &HashSet{buckets: make([][]string, capacity)}
	for _, it := range items {
		s.Add(it)
	}
	return s
}

// Add inserts v if absent.
func (s *HashSet) Add(v string) {
	i := javaSpread(javaStringHash(v)) & (len(s.buckets) - 1)
	for _, e := range s.buckets[i] {
		if e == v {
			return
		}
	}
	s.buckets[i] = append(s.buckets[i], v)
	if len(s.buckets[i]) >= 8 {
		panic("model.HashSet: bucket would be treeified in Java; order not modeled")
	}
	s.size++
	if float64(s.size) > 0.75*float64(len(s.buckets)) {
		old := s.Items()
		s.buckets = make([][]string, 2*len(s.buckets))
		s.size = 0
		for _, e := range old {
			s.Add(e)
		}
	}
}

// Items returns the elements in Java's iteration order.
func (s *HashSet) Items() []string {
	out := make([]string, 0, s.size)
	for _, b := range s.buckets {
		out = append(out, b...)
	}
	return out
}

// javaStringHash is String.hashCode: s[0]*31^(n-1) + ... over UTF-16 code units, int32.
func javaStringHash(s string) int32 {
	var h int32
	for _, u := range utf16.Encode([]rune(s)) {
		h = 31*h + int32(u)
	}
	return h
}

// javaSpread is HashMap.hash: h ^ (h >>> 16), as a non-negative table index source.
func javaSpread(h int32) int {
	u := uint32(h)
	return int(u ^ (u >> 16))
}

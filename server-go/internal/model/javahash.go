package model

import "unicode/utf16"

// JavaSet reproduces the iteration order of a java.util.HashSet (a HashMap). Java sends
// several collections in that order (supported commands, notificators, statistics per
// protocol, cell towers) and processes others in it (alarms, geofence events), and the
// order is deterministic when the element hash codes are: String.hashCode and
// Long.hashCode are fixed by the language spec, and Objects.hash combines them.
//
// The model: a power-of-two table of buckets, the spread hash h ^ (h >>> 16) masked to
// the table size, entries in insertion order within a bucket, and doubling when the size
// passes 0.75 × capacity (doubling keeps each bucket's relative order, which equals
// re-inserting in iteration order). Buckets of 8+ entries become trees in Java; with the
// small sets used here that doesn't happen, and Add panics rather than silently diverge.
type JavaSet[T any] struct {
	hash    func(T) int32
	equal   func(a, b T) bool
	buckets [][]T
	size    int
}

// NewJavaSet is new HashSet<>() (capacity 16) for elements with the given hashCode and
// equals.
func NewJavaSet[T any](hash func(T) int32, equal func(a, b T) bool) *JavaSet[T] {
	return &JavaSet[T]{hash: hash, equal: equal, buckets: make([][]T, 16)}
}

// NewJavaSetOf is new HashSet<>(collection): capacity for size/0.75 + 1 (at least 16),
// then the items added in the given order (the source collection's iteration order).
func NewJavaSetOf[T any](hash func(T) int32, equal func(a, b T) bool, items []T) *JavaSet[T] {
	want := max(int(float32(len(items))/0.75)+1, 16)
	capacity := 1
	for capacity < want {
		capacity <<= 1
	}
	s := &JavaSet[T]{hash: hash, equal: equal, buckets: make([][]T, capacity)}
	for _, it := range items {
		s.Add(it)
	}
	return s
}

// Add inserts v if absent and reports whether it did.
func (s *JavaSet[T]) Add(v T) bool {
	i := javaSpread(s.hash(v)) & (len(s.buckets) - 1)
	for _, e := range s.buckets[i] {
		if s.equal(e, v) {
			return false
		}
	}
	s.buckets[i] = append(s.buckets[i], v)
	if len(s.buckets[i]) >= 8 {
		panic("model.JavaSet: bucket would be treeified in Java; order not modeled")
	}
	s.size++
	if float64(s.size) > 0.75*float64(len(s.buckets)) {
		old := s.Items()
		s.buckets = make([][]T, 2*len(s.buckets))
		s.size = 0
		for _, e := range old {
			s.Add(e)
		}
	}
	return true
}

// Remove deletes v if present; the other entries keep their order.
func (s *JavaSet[T]) Remove(v T) {
	i := javaSpread(s.hash(v)) & (len(s.buckets) - 1)
	for k, e := range s.buckets[i] {
		if s.equal(e, v) {
			s.buckets[i] = append(s.buckets[i][:k:k], s.buckets[i][k+1:]...)
			s.size--
			return
		}
	}
}

// Contains reports whether v is in the set.
func (s *JavaSet[T]) Contains(v T) bool {
	i := javaSpread(s.hash(v)) & (len(s.buckets) - 1)
	for _, e := range s.buckets[i] {
		if s.equal(e, v) {
			return true
		}
	}
	return false
}

// Len returns the number of elements.
func (s *JavaSet[T]) Len() int { return s.size }

// Items returns the elements in Java's iteration order.
func (s *JavaSet[T]) Items() []T {
	out := make([]T, 0, s.size)
	for _, b := range s.buckets {
		out = append(out, b...)
	}
	return out
}

// HashSet is a HashSet<String>.
type HashSet = JavaSet[string]

// NewHashSet is new HashSet<String>().
func NewHashSet() *HashSet { return NewJavaSet(JavaStringHash, eq[string]) }

// NewHashSetOf is new HashSet<String>(collection).
func NewHashSetOf(items []string) *HashSet { return NewJavaSetOf(JavaStringHash, eq[string], items) }

// NewLongSet is new HashSet<Long>().
func NewLongSet() *JavaSet[int64] { return NewJavaSet(JavaLongHash, eq[int64]) }

// NewLongSetOf is new HashSet<Long>(collection).
func NewLongSetOf(items []int64) *JavaSet[int64] { return NewJavaSetOf(JavaLongHash, eq[int64], items) }

func eq[T comparable](a, b T) bool { return a == b }

// JavaLongHash is Long.hashCode: (int) (value ^ (value >>> 32)).
func JavaLongHash(v int64) int32 { return int32(uint64(v) ^ uint64(v)>>32) }

// JavaObjectsHash is Objects.hash(values...): 31 * h + element hash, from 1, with 0 for
// null. Pass the element hash codes in order.
func JavaObjectsHash(hashes ...int32) int32 {
	h := int32(1)
	for _, e := range hashes {
		h = 31*h + e
	}
	return h
}

// JavaStringHash is String.hashCode: s[0]*31^(n-1) + ... over UTF-16 code units, int32.
func JavaStringHash(s string) int32 {
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

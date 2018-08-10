package tsdb

import (
	"io"
	"sync"
	"unsafe"

	"github.com/RoaringBitmap/roaring"
)

// SeriesIDSet represents a lockable bitmap of series ids.
type SeriesIDSet struct {
	sync.RWMutex
	bitmap *roaring.Bitmap
}

// NewSeriesIDSet returns a new instance of SeriesIDSet.
func NewSeriesIDSet(a ...uint64) *SeriesIDSet {
	ss := &SeriesIDSet{bitmap: roaring.NewBitmap()}
	if len(a) > 0 {
		a32 := make([]uint32, len(a))
		for i := range a {
			a32[i] = uint32(a[i])
		}
		ss.bitmap.AddMany(a32)
	}
	return ss
}

// Bytes estimates the memory footprint of this SeriesIDSet, in bytes.
func (s *SeriesIDSet) Bytes() int {
	var b int
	s.RLock()
	b += 24 // mu RWMutex is 24 bytes
	b += int(unsafe.Sizeof(s.bitmap)) + int(s.bitmap.GetSizeInBytes())
	s.RUnlock()
	return b
}

// Add adds the series id to the set.
func (s *SeriesIDSet) Add(id SeriesID) {
	s.Lock()
	defer s.Unlock()
	s.AddNoLock(id)
}

// AddNoLock adds the series id to the set. Add is not safe for use from multiple
// goroutines. Callers must manage synchronization.
func (s *SeriesIDSet) AddNoLock(id SeriesID) {
	s.bitmap.Add(uint32(id.RawID()))
}

// Contains returns true if the id exists in the set.
func (s *SeriesIDSet) Contains(id SeriesID) bool {
	s.RLock()
	x := s.ContainsNoLock(id)
	s.RUnlock()
	return x
}

// ContainsNoLock returns true if the id exists in the set. ContainsNoLock is
// not safe for use from multiple goroutines. The caller must manage synchronization.
func (s *SeriesIDSet) ContainsNoLock(id SeriesID) bool {
	return s.bitmap.Contains(uint32(id.RawID()))
}

// Remove removes the id from the set.
func (s *SeriesIDSet) Remove(id SeriesID) {
	s.Lock()
	defer s.Unlock()
	s.RemoveNoLock(id)
}

// RemoveNoLock removes the id from the set. RemoveNoLock is not safe for use
// from multiple goroutines. The caller must manage synchronization.
func (s *SeriesIDSet) RemoveNoLock(id SeriesID) {
	s.bitmap.Remove(uint32(id.RawID()))
}

// Cardinality returns the cardinality of the SeriesIDSet.
func (s *SeriesIDSet) Cardinality() uint64 {
	s.RLock()
	defer s.RUnlock()
	return s.bitmap.GetCardinality()
}

// Merge merged the contents of others into s. The caller does not need to
// provide s as an argument, and the contents of s will always be present in s
// after Merge returns.
func (s *SeriesIDSet) Merge(others ...*SeriesIDSet) {
	bms := make([]*roaring.Bitmap, 0, len(others)+1)

	s.RLock()
	bms = append(bms, s.bitmap) // Add ourself.

	// Add other bitsets.
	for _, other := range others {
		other.RLock()
		defer other.RUnlock() // Hold until we have merged all the bitmaps
		bms = append(bms, other.bitmap)
	}

	result := roaring.FastOr(bms...)
	s.RUnlock()

	s.Lock()
	s.bitmap = result
	s.Unlock()
}

// Equals returns true if other and s are the same set of ids.
func (s *SeriesIDSet) Equals(other *SeriesIDSet) bool {
	if s == other {
		return true
	}

	s.RLock()
	defer s.RUnlock()
	other.RLock()
	defer other.RUnlock()
	return s.bitmap.Equals(other.bitmap)
}

// And returns a new SeriesIDSet containing elements that were present in s and other.
func (s *SeriesIDSet) And(other *SeriesIDSet) *SeriesIDSet {
	s.RLock()
	defer s.RUnlock()
	other.RLock()
	defer other.RUnlock()
	return &SeriesIDSet{bitmap: roaring.And(s.bitmap, other.bitmap)}
}

// AndNot returns a new SeriesIDSet containing elements that were present in s,
// but not present in other.
func (s *SeriesIDSet) AndNot(other *SeriesIDSet) *SeriesIDSet {
	s.RLock()
	defer s.RUnlock()
	other.RLock()
	defer other.RUnlock()

	return &SeriesIDSet{bitmap: roaring.AndNot(s.bitmap, other.bitmap)}
}

// ForEach calls f for each id in the set. The function is applied to the IDs
// in ascending order.
func (s *SeriesIDSet) ForEach(f func(id SeriesID)) {
	s.RLock()
	defer s.RUnlock()
	itr := s.bitmap.Iterator()
	for itr.HasNext() {
		f(NewSeriesID(uint64(itr.Next())))
	}
}

// ForEachNoLock calls f for each id in the set without taking a lock.
func (s *SeriesIDSet) ForEachNoLock(f func(id SeriesID)) {
	itr := s.bitmap.Iterator()
	for itr.HasNext() {
		f(NewSeriesID(uint64(itr.Next())))
	}
}

func (s *SeriesIDSet) String() string {
	s.RLock()
	defer s.RUnlock()
	return s.bitmap.String()
}

// Diff removes from s any elements also present in other.
func (s *SeriesIDSet) Diff(other *SeriesIDSet) {
	other.RLock()
	defer other.RUnlock()

	s.Lock()
	defer s.Unlock()
	s.bitmap = roaring.AndNot(s.bitmap, other.bitmap)
}

// Clone returns a new SeriesIDSet with a deep copy of the underlying bitmap.
func (s *SeriesIDSet) Clone() *SeriesIDSet {
	s.RLock()
	defer s.RUnlock()
	return s.CloneNoLock()
}

// CloneNoLock calls Clone without taking a lock.
func (s *SeriesIDSet) CloneNoLock() *SeriesIDSet {
	new := NewSeriesIDSet()
	new.bitmap = s.bitmap.Clone()
	return new
}

// Iterator returns an iterator to the underlying bitmap.
// This iterator is not protected by a lock.
func (s *SeriesIDSet) Iterator() SeriesIDSetIterable {
	return s.bitmap.Iterator()
}

// UnmarshalBinary unmarshals data into the set.
func (s *SeriesIDSet) UnmarshalBinary(data []byte) error {
	s.Lock()
	defer s.Unlock()
	return s.bitmap.UnmarshalBinary(data)
}

// UnmarshalBinaryUnsafe unmarshals data into the set.
// References to the underlying data are used so data should not be reused by caller.
func (s *SeriesIDSet) UnmarshalBinaryUnsafe(data []byte) error {
	s.Lock()
	defer s.Unlock()
	_, err := s.bitmap.FromBuffer(data)
	return err
}

// WriteTo writes the set to w.
func (s *SeriesIDSet) WriteTo(w io.Writer) (int64, error) {
	s.RLock()
	defer s.RUnlock()
	return s.bitmap.WriteTo(w)
}

// Slice returns a slice of series ids.
func (s *SeriesIDSet) Slice() []uint64 {
	s.RLock()
	defer s.RUnlock()

	a := make([]uint64, 0, s.bitmap.GetCardinality())
	for _, seriesID := range s.bitmap.ToArray() {
		a = append(a, uint64(seriesID))
	}
	return a
}

type SeriesIDSetIterable interface {
	HasNext() bool
	Next() uint32
}
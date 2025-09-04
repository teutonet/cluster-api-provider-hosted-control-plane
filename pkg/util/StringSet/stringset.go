package StringSet

// StringSet implements very basic set functionality for string.
//
// Internally, it uses a map[string]struct{}, which has "set"-like behaviour:
// - we can check if an element is included
// - struct{} occupies ~0 bytes.
type StringSet map[string]struct{}

// Add adds a new item to the set.
func (s StringSet) Add(item string) {
	s[item] = struct{}{}
}

// Has checks whether the item is included in the StringSet.
func (s StringSet) Has(item string) bool {
	_, has := s[item]

	return has
}

package plist

// Dict is an insertion-ordered string-keyed dictionary, like Python's dict.
// Order matters to the original project, which reads dictionaries by position
// (for example the last GPU entry in a hardware report).
type Dict struct {
	keys   []string
	values map[string]any
}

// NewDict returns an empty Dict.
func NewDict() *Dict {
	return &Dict{values: map[string]any{}}
}

// Set stores value under key. Updating an existing key keeps its position, as
// Python's dict assignment does.
func (d *Dict) Set(key string, value any) {
	if _, exists := d.values[key]; !exists {
		d.keys = append(d.keys, key)
	}
	d.values[key] = value
}

// Get returns the value stored under key.
func (d *Dict) Get(key string) (any, bool) {
	v, ok := d.values[key]
	return v, ok
}

// Keys returns the keys in insertion order.
func (d *Dict) Keys() []string {
	return append([]string(nil), d.keys...)
}

// Len returns the number of entries.
func (d *Dict) Len() int {
	return len(d.keys)
}

package plist

// Delete removes key from the dictionary, keeping the order of the remaining
// keys, as Python's del does. It does nothing when the key is absent.
func (d *Dict) Delete(key string) {
	if _, exists := d.values[key]; !exists {
		return
	}
	delete(d.values, key)
	for i, k := range d.keys {
		if k == key {
			d.keys = append(d.keys[:i], d.keys[i+1:]...)
			break
		}
	}
}

package plist

import (
	"reflect"
	"testing"
	"time"
)

// TestRoundTrip checks that Unmarshal(Marshal(v)) returns the same values, with
// keys in the sorted order that Marshal writes.
func TestRoundTrip(t *testing.T) {
	d := NewDict()
	d.Set("zeta", int64(1))
	d.Set("alpha", []any{"x", []byte{1, 2, 3}, true})
	d.Set("nested", NewDict())
	d.Set("when", time.Date(2021, 6, 7, 8, 9, 10, 0, time.UTC))
	d.Set("ratio", 0.25)

	data, err := Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	gd, ok := got.(*Dict)
	if !ok {
		t.Fatalf("root is %T; want *Dict", got)
	}
	// dumps sorts keys by default (sort_keys=True), so the read-back order is sorted.
	if want := []string{"alpha", "nested", "ratio", "when", "zeta"}; !reflect.DeepEqual(gd.Keys(), want) {
		t.Errorf("key order = %v; want %v", gd.Keys(), want)
	}
	if v, _ := gd.Get("alpha"); !reflect.DeepEqual(v, []any{"x", []byte{1, 2, 3}, true}) {
		t.Errorf("alpha = %#v", v)
	}
	if v, _ := gd.Get("ratio"); v != 0.25 {
		t.Errorf("ratio = %#v", v)
	}
}

// TestMarshalSortsKeys checks that dictionary keys are written sorted, as
// plistlib does by default, regardless of insertion order.
func TestMarshalSortsKeys(t *testing.T) {
	d := NewDict()
	d.Set("b", int64(2))
	d.Set("a", int64(1))
	out, err := Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	// plistlib does not indent the root value, so <dict> sits at column 0.
	want := plistHeader + "<plist version=\"1.0\">\n<dict>\n\t<key>a</key>\n\t<integer>1</integer>\n" +
		"\t<key>b</key>\n\t<integer>2</integer>\n</dict>\n</plist>\n"
	if string(out) != want {
		t.Errorf("Marshal =\n%s\nwant\n%s", out, want)
	}
}

// TestUnmarshalBinaryRejected documents the one unsupported format. The error is
// explicit instead of a silent misparse.
func TestUnmarshalBinaryRejected(t *testing.T) {
	if _, err := Unmarshal([]byte("bplist00\x00")); err == nil {
		t.Error("binary plist accepted; want explicit error")
	}
}

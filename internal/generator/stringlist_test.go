package generator

import "testing"

// --- encodeStringList — mirrors StringListConverter.convertToDatabaseColumn.
// This service never reads a movie back out, so there's no decodeStringList
// counterpart to test here. ---

func TestEncodeStringList_Nil(t *testing.T) {
	if got := encodeStringList(nil); got != "{}" {
		t.Errorf("encodeStringList(nil) = %q, want \"{}\"", got)
	}
}

func TestEncodeStringList_Empty(t *testing.T) {
	if got := encodeStringList([]string{}); got != "{}" {
		t.Errorf("encodeStringList([]) = %q, want \"{}\"", got)
	}
}

func TestEncodeStringList_SingleItem(t *testing.T) {
	got := encodeStringList([]string{"https://youtube.com/watch?v=abc"})
	want := "{https://youtube.com/watch?v=abc}"
	if got != want {
		t.Errorf("encodeStringList() = %q, want %q", got, want)
	}
}

func TestEncodeStringList_MultipleItems(t *testing.T) {
	got := encodeStringList([]string{"a", "b", "c"})
	want := "{a,b,c}"
	if got != want {
		t.Errorf("encodeStringList() = %q, want %q", got, want)
	}
}

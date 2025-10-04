package state

import "testing"

func TestAppendRootChanges(t *testing.T) {
	tr := New()
	prev := tr.Root()
	for i := 0; i < 10; i++ {
		root := tr.Append([]byte{byte(i)})
		if root == prev {
			t.Fatalf("root did not change on append %d", i)
		}
		prev = root
	}
}

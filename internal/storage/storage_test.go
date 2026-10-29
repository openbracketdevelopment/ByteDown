package storage

import "testing"

func TestPathsAndReadiness(t *testing.T) {
	root := t.TempDir()
	if e := Init(root); e != nil {
		t.Fatal(e)
	}
	if e := Check(root); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"../outside", "/etc/passwd", ""} {
		if _, e := Path(root, path); e == nil {
			t.Fatalf("accepted unsafe path %q", path)
		}
	}
	if ID() == ID() {
		t.Fatal("IDs collide")
	}
}

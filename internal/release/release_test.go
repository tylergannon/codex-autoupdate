package release

import "testing"

func TestCompareNumericVersions(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		left, right string
		want        int
	}{
		{"2", "1", 1},
		{"1.24012.1", "1.24012.1", 0},
		{"1.24012.1", "1.2409.9", 1},
		{"1.2", "1.2.0", 0},
		{"1.2.0", "1.2.1", -1},
	} {
		got, err := Compare(test.left, test.right)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("Compare(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestCompareRejectsInvalidVersions(t *testing.T) {
	t.Parallel()
	if _, err := Compare("1.beta", "2"); err == nil {
		t.Fatal("expected invalid version error")
	}
}

package semver

import "testing"

func TestParse(t *testing.T) {
	valid := map[string]Version{
		"1.2.3":    {Major: 1, Minor: 2, Patch: 3},
		"v1.2.3":   {Major: 1, Minor: 2, Patch: 3},
		"v10.0.12": {Major: 10, Minor: 0, Patch: 12},
	}

	for value, expected := range valid {
		version, err := Parse(value)

		if err != nil || version != expected {
			t.Errorf("Parse(%q) = %v, %v; expected %v", value, version, err, expected)
		}
	}

	for _, value := range []string{"", "1.2", "1.2.3.4", "v1.2.3-rc.1", "1.2.3+build", "1.x.3", "latest", "V1.2.3", "1.-2.3"} {
		if _, err := Parse(value); err == nil {
			t.Errorf("Parse(%q) should have failed", value)
		}
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b     string
		expected int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.0.9", "1.0.10", -1},
		{"1.9.0", "1.10.0", -1},
		{"2.0.0", "1.99.99", 1},
	}

	for _, c := range cases {
		a, _ := Parse(c.a)
		b, _ := Parse(c.b)

		if result := a.Compare(b); result != c.expected {
			t.Errorf("%s.Compare(%s) = %d; expected %d", c.a, c.b, result, c.expected)
		}
	}
}

func TestChange(t *testing.T) {
	cases := []struct {
		from, to string
		expected string
	}{
		{"1.0.0", "2.0.0", "major"},
		{"1.4.7", "2.0.0", "major"},
		{"1.0.0", "1.1.0", "minor"},
		{"1.0.3", "1.1.0", "minor"},
		{"1.0.0", "1.0.1", "patch"},
		{"1.0.0", "1.0.0", ""},
	}

	for _, c := range cases {
		from, _ := Parse(c.from)
		to, _ := Parse(c.to)

		if result := from.Change(to); result != c.expected {
			t.Errorf("%s.Change(%s) = %q; expected %q", c.from, c.to, result, c.expected)
		}
	}
}

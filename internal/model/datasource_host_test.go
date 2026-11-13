package model

import "testing"

func TestPublicDataSourceHost(t *testing.T) {
	cases := []struct{ in, want string }{
		{"db.example:5432", "db.example:5432"},
		{"https://lake.example/path", "https://lake.example/path"},
		{"https://user:password@lake.example/path?signature=secret&region=us#fragment", "https://redacted@lake.example/path?redacted#redacted"},
		{"https://lake.example/path?region=us", "https://lake.example/path?redacted"},
		{"user:password@host", "[redacted]"},
		{"https://invalid%zz?token=secret", "[redacted]"},
	}
	for _, c := range cases {
		if got := PublicDataSourceHost(c.in); got != c.want {
			t.Errorf("PublicDataSourceHost(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

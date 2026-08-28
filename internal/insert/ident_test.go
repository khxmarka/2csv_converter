package insert

import "testing"

func TestCleanIdent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"`users`", "users"},
		{`"users"`, "users"},
		{"[users]", "users"},
		{"first/name", "firstname"},
		{`\col\`, "col"},
		{"  id  ", "id"},
		{"`a/b`", "ab"},
	}
	for _, tc := range cases {
		if got := cleanIdent(tc.in); got != tc.want {
			t.Fatalf("cleanIdent(%q)=%q, ожидалось %q", tc.in, got, tc.want)
		}
	}
}

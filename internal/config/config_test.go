package config

import "testing"

func TestRootFor(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "db", want: RootDB},
		{in: "DB", want: RootDB},
		{in: " Db ", want: RootDB},
		{in: "combo", want: RootCombo},
		{in: "COMBO", want: RootCombo},
		{in: " combo ", want: RootCombo},
		{in: "", wantErr: true},
		{in: "   ", wantErr: true},
		{in: "nope", wantErr: true},
		{in: "db combo", wantErr: true},
	}
	for _, tc := range cases {
		got, err := RootFor(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("RootFor(%q): ожидалась ошибка, получено %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("RootFor(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("RootFor(%q)=%q, ожидалось %q", tc.in, got, tc.want)
		}
	}
}

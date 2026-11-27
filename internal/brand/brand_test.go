package brand

import "testing"

func TestProfileIsUsable(t *testing.T) {
	if Current.Name == "" || Current.Tagline == "" || Current.Description == "" {
		t.Fatalf("brand.json 字段不能为空：%+v", Current)
	}
}

func TestTitleKeepsProductNameVisible(t *testing.T) {
	cases := []struct {
		node string
		want string
	}{
		{"", Name()},
		{"   ", Name()},
		{"书房 PC", Name() + " · 书房 PC"},
		{" 书房 PC ", Name() + " · 书房 PC"},
		{Name(), Name()},
	}
	for _, tc := range cases {
		if got := Title(tc.node); got != tc.want {
			t.Fatalf("Title(%q) = %q, want %q", tc.node, got, tc.want)
		}
	}
}

package deploy

import "testing"

func TestNormalizedPrefix(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "/mindfs", want: "/mindfs"},
		{in: "/mindfs/", want: "/mindfs"},
		{in: "mindfs", want: "/mindfs"},
		{in: "/", want: ""},
		{in: "", want: ""},
		{in: "  /x/  ", want: "/x"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			prev := Prefix
			Prefix = tt.in
			defer func() { Prefix = prev }()
			if got := NormalizedPrefix(); got != tt.want {
				t.Fatalf("NormalizedPrefix() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrefixedPath(t *testing.T) {
	tests := []struct {
		prefix string
		in     string
		want   string
	}{
		{prefix: "/mindfs", in: "/api/tree", want: "/mindfs/api/tree"},
		{prefix: "/mindfs", in: "api/tree", want: "/mindfs/api/tree"},
		{prefix: "", in: "/api/tree", want: "/api/tree"},
	}
	for _, tt := range tests {
		t.Run(tt.prefix+tt.in, func(t *testing.T) {
			prev := Prefix
			Prefix = tt.prefix
			defer func() { Prefix = prev }()
			if got := PrefixedPath(tt.in); got != tt.want {
				t.Fatalf("PrefixedPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRelayAssetsAlias(t *testing.T) {
	tests := []struct {
		prefix string
		want   string
	}{
		{prefix: "/mindfs", want: "/mindfs-assets/"},
		{prefix: "/app", want: "/app-assets/"},
		{prefix: "", want: "/assets/"},
	}
	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			prev := Prefix
			Prefix = tt.prefix
			defer func() { Prefix = prev }()
			if got := RelayAssetsAlias(); got != tt.want {
				t.Fatalf("RelayAssetsAlias() = %q, want %q", got, tt.want)
			}
		})
	}
}

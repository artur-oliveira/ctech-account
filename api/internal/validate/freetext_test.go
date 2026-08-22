package validate

import "testing"

func TestFreetext(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		rule    FreetextRule
		want    string
		wantErr bool
	}{
		{"trims whitespace", "  hello world  ", FreetextRule{Min: 3, Max: 100}, "hello world", false},
		{"too short after trim", "  hi  ", FreetextRule{Min: 15, Max: 100}, "", true},
		{"too long", string(make([]byte, 200)), FreetextRule{Min: 1, Max: 100}, "", true},
		{"repeated character run", "aaaaaaaaaaaaaaaaaaaa", FreetextRule{Min: 3, Max: 100}, "", true},
		{"repeated punctuation run", "..........", FreetextRule{Min: 3, Max: 100}, "", true},
		{"no letters at all", "1234567890123456", FreetextRule{Min: 3, Max: 100}, "", true},
		{"real sentence passes", "O botão de login não funciona no Safari", FreetextRule{Min: 15, Max: 4000}, "O botão de login não funciona no Safari", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Freetext(tc.in, tc.rule)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (result %q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

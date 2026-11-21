package source

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		in      string
		want    Ref
		str     string
		wantErr string
	}{
		{in: "36451", want: Ref{Anna, "36451", ""}, str: "36451"},
		{in: " 36451-the-hobbit ", want: Ref{Anna, "36451", ""}, str: "36451"},
		{in: "anna:36451", want: Ref{Anna, "36451", ""}, str: "36451"},
		{in: "zlibrary:1234/ab12cd", want: Ref{ZLibrary, "1234", "ab12cd"}, str: "zlibrary:1234/ab12cd"},
		{in: "irc:16d6770d2ba9", want: Ref{IRC, "16d6770d2ba9", ""}, str: "irc:16d6770d2ba9"},
		{in: "irc:16d6770d2ba9/ff", want: Ref{IRC, "16d6770d2ba9", "ff"}, str: "irc:16d6770d2ba9/ff"},
		{in: "zlibrary:1234", wantErr: "source: zlibrary reference needs a hash"},
		{in: "anna:1234/abc", wantErr: "source: anna reference takes no hash"},
		{in: "smug:1234", wantErr: `source: unknown source "smug"`},
		{in: "", wantErr: "source: empty reference"},
		{in: "  ", wantErr: "source: empty reference"},
		{in: "36451-", wantErr: `source: invalid book id "36451-"`},
		{in: "12a34", wantErr: `source: invalid book id "12a34"`},
		{in: "1234/ab", wantErr: `source: invalid book id "1234/ab"`},
		{in: "zlibrary:12/ab/cd", wantErr: `source: invalid hash "ab/cd"`},
		{in: "anna:1234-slug", wantErr: `source: invalid book id "1234-slug"`},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := Parse(tc.in)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("Parse(%q) error = %v, want %q", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("Parse(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
			if got.String() != tc.str {
				t.Errorf("String() = %q, want %q", got.String(), tc.str)
			}
			if again, err := Parse(got.String()); err != nil || again != got {
				t.Errorf("round trip = %+v, %v; want %+v", again, err, got)
			}
			if err := got.Validate(); err != nil {
				t.Errorf("Validate() = %v", err)
			}
		})
	}
}

func TestKeyDistinguishes(t *testing.T) {
	a, b, c := Ref{Anna, "1234", ""}, Ref{ZLibrary, "1234", "ab"}, Ref{Anna, "1234", "x"}
	if a.Key() == b.Key() || a.Key() == c.Key() || b.Key() == c.Key() {
		t.Fatalf("keys collide: %q %q %q", a.Key(), b.Key(), c.Key())
	}
}

func TestValidateLiteral(t *testing.T) {
	for _, r := range []Ref{{}, {Anna, "1", "h"}, {ZLibrary, "1", ""}, {Name("x"), "1", ""}} {
		if r.Validate() == nil {
			t.Errorf("Validate(%+v) = nil, want error", r)
		}
	}
	if !(Ref{}).IsZero() || (Ref{Anna, "1", ""}).IsZero() {
		t.Error("IsZero wrong")
	}
}

package invite

import "testing"

const (
	code  = "AB23CD"
	token = "0123456789abcdef0123456789abcdef"
)

func TestFormatAndParse(t *testing.T) {
	got := Format(code, token)
	want := "gamelink://join/" + code + "/" + token
	if got != want {
		t.Fatalf("format %s", got)
	}
	if !IsURL(got) || !IsURL("  "+got+"  ") {
		t.Fatal("is url")
	}
	samples := []string{
		got,
		`"` + got + `"`,
		"  " + got + "  ",
		"GAMELINK://JOIN/" + code + "/" + token,
		code + " " + token,
		code + "/" + stringsUpper(token),
	}
	for _, sample := range samples {
		c, tok, err := Parse(sample)
		if err != nil || c != code || tok != token {
			t.Fatalf("%q -> %s %s %v", sample, c, tok, err)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, sample := range []string{"", "AB23CD", "gamelink://join/IIIIII/" + token, "not a link"} {
		if _, _, err := Parse(sample); err == nil {
			t.Fatalf("accepted %q", sample)
		}
	}
}

func stringsUpper(s string) string {
	return "0123456789ABCDEF0123456789ABCDEF"
}

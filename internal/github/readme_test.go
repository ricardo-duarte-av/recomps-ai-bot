package github

import "testing"

func TestParseRepo(t *testing.T) {
	want := "https://github.com/zeldaret/oot"
	for _, in := range []string{
		"https://www.github.com/zeldaret/oot",
		"https://github.com/ZeldaRet/OoT.git",
		"github.com/zeldaret/oot/tree/main/src",
		"http://github.com/zeldaret/oot?tab=readme#build",
		"<https://github.com/zeldaret/oot>",
		"https://github.com/zeldaret/oot/,",
	} {
		r, err := ParseRepo(in)
		if err != nil || r.URL() != want {
			t.Errorf("ParseRepo(%q) = %q, %v", in, r.URL(), err)
		}
	}
	for _, in := range []string{"https://gitlab.com/a/b", "https://github.com/onlyowner", "nope"} {
		if _, err := ParseRepo(in); err == nil {
			t.Errorf("ParseRepo(%q) should fail", in)
		}
	}
}

func TestFindRepo(t *testing.T) {
	r, ok := FindRepo("bot add this repo to the list https://www.github.com/pret/pokeemerald , a gba decomp")
	if !ok || r.URL() != "https://github.com/pret/pokeemerald" {
		t.Errorf("got %q %v", r.URL(), ok)
	}
}

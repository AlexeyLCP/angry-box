package ssh

import "testing"

func TestQuotePOSIX(t *testing.T) {
	got, err := QuotePOSIX("/etc/sing-box/config.json")
	if err != nil {
		t.Fatal(err)
	}
	if got != `'/etc/sing-box/config.json'` {
		t.Fatalf("got %q", got)
	}
	got, err = QuotePOSIX("foo'bar")
	if err != nil {
		t.Fatal(err)
	}
	if got != `'foo'"'"'bar'` {
		t.Fatalf("got %q", got)
	}
	if _, err := QuotePOSIX(""); err == nil {
		t.Fatal("empty must fail")
	}
	if _, err := QuotePOSIX("a\nb"); err == nil {
		t.Fatal("newline must fail")
	}
	if _, err := QuotePOSIX("/tmp/x; id; #"); err != nil {
		t.Fatal(err)
	}
}

func TestQuotePOSIX_MetacharactersStayInsideQuotes(t *testing.T) {
	in := ` /tmp/x; id; # `
	got, err := QuotePOSIX(in)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != '\'' || got[len(got)-1] != '\'' {
		t.Fatalf("not fully quoted: %q", got)
	}
}

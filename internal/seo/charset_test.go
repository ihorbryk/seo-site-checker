package seo

import "testing"

func TestDecode(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        []byte
		want        string
	}{
		{"utf-8 default", "text/html", []byte("caf\xc3\xa9"), "café"},
		{"content-type latin1", "text/html; charset=iso-8859-1", []byte("caf\xe9"), "café"},
		{"content-type wins over meta", "text/html; charset=utf-8", []byte(`<meta charset="iso-8859-1">caf` + "\xc3\xa9"), `<meta charset="iso-8859-1">café`},
		{"meta charset", "text/html", []byte(`<meta charset="windows-1251"><p>` + "\xcf\xf0\xe8\xe2\xe5\xf2"), `<meta charset="windows-1251"><p>Привет`},
		{"meta http-equiv", "text/html", []byte(`<meta http-equiv="Content-Type" content="text/html; charset=iso-8859-1">` + "\xe9"), `<meta http-equiv="Content-Type" content="text/html; charset=iso-8859-1">é`},
		{"unknown content-type charset falls to meta", "text/html; charset=bogus", []byte(`<meta charset="iso-8859-1">` + "\xe9"), `<meta charset="iso-8859-1">é`},
		{"unknown everywhere falls to utf-8", "text/html; charset=bogus", []byte("caf\xc3\xa9"), "café"},
		{"invalid utf-8 replaced", "text/html", []byte("a\xffb"), "a�b"},
		{"invalid utf-8 with declared utf-8", "text/html; charset=utf-8", []byte("a\xc3"), "a�"},
		{"bom stripped", "text/html", []byte("\xef\xbb\xbfhi"), "hi"},
		{"meta beyond 1024 bytes ignored", "text/html", append([]byte(string(make([]byte, 0))+spaces(1100)), []byte(`<meta charset="iso-8859-1">`+"\xc3\xa9")...), spaces(1100) + `<meta charset="iso-8859-1">é`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Decode(tt.contentType, tt.body); got != tt.want {
				t.Errorf("Decode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func spaces(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	return string(b)
}

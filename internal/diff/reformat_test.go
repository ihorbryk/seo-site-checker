package diff

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestReformatGolden(t *testing.T) {
	for _, name := range []string{"minified", "pretty"} {
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("testdata", name+".html"))
			if err != nil {
				t.Fatal(err)
			}
			got := Reformat(string(src))
			golden := filepath.Join("testdata", name+".golden")
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("%s differs (run with -update to accept):\n%s", golden, got)
			}
		})
	}
}

func TestReformatMakesMinifiedAndPrettyMarkupEqual(t *testing.T) {
	a := "<div><p>Hello   world</p></div>"
	b := "<div>\n  <p>\n    Hello world\n  </p>\n</div>\n"
	if Reformat(a) != Reformat(b) {
		t.Errorf("%q != %q", Reformat(a), Reformat(b))
	}
}

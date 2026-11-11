package diff

import "testing"

func TestBuiltinMasks(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"nonce double quoted", `<script nonce="abc123" src="/a.js">`, `<script ⟨masked⟩ src="/a.js">`},
		{"nonce single quoted", `<style NONCE='x y'>`, `<style ⟨masked⟩>`},
		{"nonce unquoted", `<script nonce=abc src="/a.js">`, `<script ⟨masked⟩ src="/a.js">`},
		{"csrf hidden input, type first", `<input type="hidden" name="csrf_token" value="abc">`, `⟨masked⟩`},
		{"csrf hidden input, name first", `<input name="_csrf" value="abc" type="hidden">`, `⟨masked⟩`},
		{"authenticity token", `<input type="hidden" name="authenticity_token" value="z">`, `⟨masked⟩`},
		{"csrf meta tag", `<meta name="csrf-token" content="abc">`, `⟨masked⟩`},
		{"xsrf meta tag", `<meta content="abc" name="xsrf-token">`, `⟨masked⟩`},
		{"visible csrf-named input is real content", `<input type="text" name="csrf_token" value="abc">`, `<input type="text" name="csrf_token" value="abc">`},
		{"ordinary hidden input stays", `<input type="hidden" name="product" value="7">`, `<input type="hidden" name="product" value="7">`},
		{"description meta stays", `<meta name="description" content="x">`, `<meta name="description" content="x">`},
		{"each match is masked", "<script nonce=\"a\">\n<style nonce=\"b\">", "<script ⟨masked⟩>\n<style ⟨masked⟩>"},
	}
	masks := BuiltinRules().Masks()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ApplyMasks(tt.in, masks); got != tt.want {
				t.Errorf("ApplyMasks(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestProjectMaskAndInvalidMask(t *testing.T) {
	rules := Rules{
		{Kind: KindBodyMask, Value: `build-\d+`},
		{Kind: KindBodyMask, Value: `(`}, // invalid: validated when saved, skipped here
	}
	if got := ApplyMasks("v build-42 and build-7", rules.Masks()); got != "v ⟨masked⟩ and ⟨masked⟩" {
		t.Errorf("got %q", got)
	}
}

func TestIgnoredHeadersAreLowercasedAndBuiltinCoversSpecList(t *testing.T) {
	got := map[string]bool{}
	for _, h := range BuiltinRules().IgnoredHeaders() {
		got[h] = true
	}
	for _, want := range []string{"date", "age", "expires", "set-cookie", "etag", "last-modified", "cf-ray", "x-request-id", "server-timing", "report-to", "nel"} {
		if !got[want] {
			t.Errorf("built-in rules do not ignore %q", want)
		}
	}
}

func TestRulesRoundTripThroughJSON(t *testing.T) {
	raw, err := BuiltinRules().JSON()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseRules(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(back) != len(BuiltinRules()) {
		t.Errorf("round trip lost rules: %d != %d", len(back), len(BuiltinRules()))
	}
}

// Package seo decodes stored HTML and extracts a Snapshot's SEO fields.
package seo

import (
	"bytes"
	"mime"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"
)

// metaScanLimit is how far into the body a <meta charset> is looked for.
const metaScanLimit = 1024

// Decode turns a stored body into a UTF-8 string for extraction, display and
// diffing. The charset comes from the Content-Type, then a <meta charset> in
// the first 1024 bytes, then UTF-8. Invalid bytes become U+FFFD. The stored
// bytes are never changed.
func Decode(contentType string, body []byte) string {
	enc := encodingFor(contentType, body)
	out, err := enc.NewDecoder().Bytes(body)
	if err != nil {
		out = bytes.ToValidUTF8(body, []byte("�"))
	}
	return strings.TrimPrefix(string(out), "\xef\xbb\xbf")
}

func encodingFor(contentType string, body []byte) encoding.Encoding {
	if _, params, err := mime.ParseMediaType(contentType); err == nil {
		if e := lookup(params["charset"]); e != nil {
			return e
		}
	}
	if e := lookup(metaCharset(body)); e != nil {
		return e
	}
	return unicode.UTF8
}

func lookup(label string) encoding.Encoding {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil
	}
	e, name := charset.Lookup(label)
	if e == nil || strings.HasPrefix(name, "utf-16") {
		return nil
	}
	return e
}

// metaCharset finds the first charset declared by a <meta> in the head of the body.
func metaCharset(body []byte) string {
	if len(body) > metaScanLimit {
		body = body[:metaScanLimit]
	}
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "meta" || !hasAttr {
				continue
			}
			attrs := map[string]string{}
			for {
				k, v, more := z.TagAttr()
				attrs[string(k)] = string(v)
				if !more {
					break
				}
			}
			if cs := attrs["charset"]; cs != "" {
				return cs
			}
			if strings.EqualFold(attrs["http-equiv"], "content-type") {
				if _, params, err := mime.ParseMediaType(attrs["content"]); err == nil && params["charset"] != "" {
					return params["charset"]
				}
			}
		}
	}
}

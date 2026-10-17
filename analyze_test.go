package main

import (
	"html"
	"regexp"
	"strings"
	"testing"
)

func TestSanitizeHTMLKeepsLibraryMarkup(t *testing.T) {
	cases := []struct{ in, want string }{
		{`<span class="good">Yes</span>`, `<span class="good">Yes</span>`},
		{`<span class='log4 log5'>Drive</span>`, `<span class="log4 log5">Drive</span>`},
		{`<span class ="bad">No</span>`, `<span class="bad">No</span>`},
		{`<span>plain</span>`, `<span>plain</span>`},
		{`<strong>x</strong><br>`, `<strong>x</strong><br>`},
		{`<div class="bad">Notice</div>`, `<div class="bad">Notice</div>`},
		{"<span class=\"log3\">a\nb</span>", "<span class=\"log3\">a\nb</span>"},
	}
	for _, c := range cases {
		if got := sanitizeHTML(c.in); got != c.want {
			t.Errorf("sanitizeHTML(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeHTMLEscapesEverythingElse(t *testing.T) {
	cases := []struct{ in, want string }{
		{`<script>alert(1)</script>`, `&lt;script&gt;alert(1)&lt;/script&gt;`},
		{`<img src=x onerror=alert(1)>`, `&lt;img src=x onerror=alert(1)&gt;`},
		// Event handlers on otherwise-allowed tags, in every spelling.
		{`<span onclick="alert(1)">x</span>`, `&lt;span onclick="alert(1)"&gt;x`},
		{`<span onclick ="alert(1)">x</span>`, `&lt;span onclick ="alert(1)"&gt;x`},
		{`<span/onclick=alert(1)>x</span>`, `&lt;span/onclick=alert(1)&gt;x`},
		{`<span class="good" onmouseover=alert(1)>x</span>`, `&lt;span class="good" onmouseover=alert(1)&gt;x`},
		{`<span class="good"style="color:red">x</span>`, `&lt;span class="good"style="color:red"&gt;x`},
		{`<SPAN class="good">x</SPAN>`, `&lt;SPAN class="good"&gt;x&lt;/SPAN&gt;`},
		// Unknown or crafted classes.
		{`<span class="good evil">x</span>`, `&lt;span class="good evil"&gt;x`},
		{`<span class="log-modal open">x</span>`, `&lt;span class="log-modal open"&gt;x`},
		// Breaking out of the <pre> wrapper of the result page.
		{`</pre><a href="//evil">x</a>`, `&lt;/pre&gt;&lt;a href="//evil"&gt;x&lt;/a&gt;`},
		// Entities in the log are shown literally, not decoded.
		{`&lt;script&gt;`, `&amp;lt;script&amp;gt;`},
		{`a > b & c < d`, `a &gt; b &amp; c &lt; d`},
		{`<!-- x --><style>`, `&lt;!-- x --&gt;&lt;style&gt;`},
		// Real XLD filenames (logchecker-go sample angle_bracket.log).
		{`<span class="log3">03 - Signe <記号>.flac</span>`, `<span class="log3">03 - Signe &lt;記号&gt;.flac</span>`},
	}
	for _, c := range cases {
		if got := sanitizeHTML(c.in); got != c.want {
			t.Errorf("sanitizeHTML(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeHTMLBalancesTags(t *testing.T) {
	cases := []struct{ in, want string }{
		{`</span>x`, `x`}, // stray close dropped
		{`<span class="good">x`, `<span class="good">x</span>`}, // left open
		{`<div><span>x</div>y`, `<div><span>x</span></div>y`},   // misnested
		{`<strong>a</span>b</strong>`, `<strong>ab</strong>`},   // close of a tag never opened
		{`<span><span class="bad">a</span></span>`, `<span><span class="bad">a</span></span>`},
	}
	for _, c := range cases {
		if got := sanitizeHTML(c.in); got != c.want {
			t.Errorf("sanitizeHTML(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

var (
	reAnyTagOut = regexp.MustCompile(`<[^>]*>`)
	reSafeTag   = regexp.MustCompile(`^</?(?:span|div|p|strong|em|br)(?: class="(?:good|goodish|bad|badish|log[1-5])(?: (?:good|goodish|bad|badish|log[1-5]))*")?>$`)
)

// assertSafeHTML fails if out contains anything other than allowlisted tags
// and escaped text.
func assertSafeHTML(t *testing.T, in, out string) {
	t.Helper()
	for _, tag := range reAnyTagOut.FindAllString(out, -1) {
		if !reSafeTag.MatchString(tag) {
			t.Fatalf("unsafe tag %q in output for input %q", tag, in)
		}
	}
	text := html.UnescapeString(reAnyTagOut.ReplaceAllString(out, ""))
	if strings.Count(text, "<")+strings.Count(text, ">") > strings.Count(in, "<")+strings.Count(in, ">") {
		t.Fatalf("sanitizer invented < or > for input %q: %q", in, out)
	}
	if strings.ContainsAny(reAnyTagOut.ReplaceAllString(out, ""), "<>") {
		t.Fatalf("unescaped < or > in text for input %q: %q", in, out)
	}
}

func FuzzSanitizeHTML(f *testing.F) {
	for _, s := range []string{
		`<span class="good">ok</span>`,
		`<span class ='bad'>x</span>`,
		`<script>alert(1)</script>`,
		`<span onclick ="alert(1)">x</span>`,
		`<span class="good"/onmouseover=1>`,
		`<<span>>`,
		`<span class="good"`,
		"<div class=\"bad\">\n<br/></div>",
		`&amp;<&>`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		assertSafeHTML(t, in, sanitizeHTML(in))
	})
}

func TestContainsMarkup(t *testing.T) {
	yes := []string{
		`<span class="good">`, `</span>`, `<SPAN>`, `<br>`, `<br/>`, `<div class="bad">`,
		"<strong\n>", `<p>`, `<em>`,
	}
	no := []string{
		"Exact Audio Copy V1.6 from 23. October 2020",
		"Track 01 <3 love song.flac",
		"<pre>", "<spanner>", "a < b > c", "<ab>", "C:\\Music\\<Various>",
		"Filename : /konwersja/03 - Signe <記号>.flac",
	}
	for _, s := range yes {
		if !containsMarkup(s) {
			t.Errorf("containsMarkup(%q) = false, want true", s)
		}
	}
	for _, s := range no {
		if containsMarkup(s) {
			t.Errorf("containsMarkup(%q) = true, want false", s)
		}
	}
}

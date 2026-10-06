package newsletter

import (
	"strings"
	"testing"
)

func TestSanitizeHTML(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"drops handlers and script content", `<p onclick="x">Hi <script>alert(1)</script><b>there</b></p>`, `<p>Hi <b>there</b></p>`},
		{"unwraps javascript links", `<a href="javascript:x">t</a>`, `t`},
		{"keeps only href on links", `<a href="https://a.io" target="_blank" style="x">t</a>`, `<a href="https://a.io">t</a>`},
		{"drops images", `<img src=x onerror=y>`, ``},
		{"drops style content", `<div><style>p{}</style>Ok</div>`, `<div>Ok</div>`},
		{"keeps escaped text escaped", `<p>&lt;b&gt;</p>`, `<p>&lt;b&gt;</p>`},
		{"unwraps tables", `<table><tr><td>Cell</td></tr></table>`, `Cell`},
		{"keeps lists and breaks", `<ul><li>a<br>b</li></ul>`, `<ul><li>a<br>b</li></ul>`},
		{"closes unclosed tags", `<p><strong>open`, `<p><strong>open</strong></p>`},
		{"drops stray end tags", `a</p></div>b`, `ab`},
		{"mailto allowed", `<a href="mailto:tips@thetriangle.org">tip</a>`, `<a href="mailto:tips@thetriangle.org">tip</a>`},
		{"data urls unwrapped", `<a href="data:text/html;base64,xx">t</a>`, `t`},
		{"escapes quotes in href", `<a href='https://a.io/?q="x"'>t</a>`, `<a href="https://a.io/?q=&#34;x&#34;">t</a>`},
		{"iframe content dropped", `<iframe src="https://evil">inner</iframe>ok`, `ok`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeHTML(tc.in); got != tc.want {
				t.Errorf("SanitizeHTML(%q)\n got: %q\nwant: %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeHTML_NeverEmitsScript(t *testing.T) {
	for _, in := range []string{
		`<scr<script>ipt>alert(1)</script>`,
		`<svg><script>alert(1)</script></svg>`,
		`<a href="java&#x09;script:alert(1)">x</a>`,
		`<a href=" javascript:alert(1)">x</a>`,
		`<<script>script>alert(1)<</script>/script>`,
	} {
		out := strings.ToLower(SanitizeHTML(in))
		if strings.Contains(out, "<script") || strings.Contains(out, "javascript:") || strings.Contains(out, "<svg") {
			t.Errorf("SanitizeHTML(%q) = %q", in, out)
		}
	}
}

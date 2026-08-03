package database

import (
	"strings"
	"testing"
)

// embeddingSourceGoldens pins the exact text the CMS embeds, by hash.
//
// The WordPress ETL bulk-loads vectors for the migrated archive and writes these
// same hashes, so the two implementations must agree byte for byte. When they do
// not, nothing fails: the reconciler simply decides every seeded row is stale and
// re-embeds the whole archive through the sidecar after every reseed.
//
// The identical table lives in wordpress-etl tests/test_article_embeddings.py.
// A change here that is not mirrored there breaks the contract, and vice versa.
var embeddingSourceGoldens = []struct {
	name  string
	title string
	tags  string
	body  string
	hash  string
	runes int
}{
	{
		name:  "tags and entity-decoded body",
		title: "Tuition freeze",
		tags:  "campus,money",
		body:  "<p>Hello &amp; welcome</p><div>world</div>",
		hash:  "ef099c229d1e5a3b4541abe9c57491b2467711de61aab5124bc1724dc7f9bcbd",
		runes: 51,
	},
	{
		name:  "empty parts are dropped, not joined as blanks",
		title: "  Spaced  ",
		tags:  "",
		body:  "<p>a</p>\n\n<p>b</p>",
		hash:  "55b414e3ba68401d30466d433fa01cbf79a3352cc2cfd5ae5c379ae8541d9013",
		runes: 11,
	},
	{
		name:  "non-ASCII survives intact",
		title: "Unicode café — naïve",
		tags:  "résumé",
		body:  "<p>éèê x</p>",
		hash:  "dc79ead758b72f0e0cf244f00c419fde0eb64b78ad288d18e0dbb8ea88d6c64f",
		runes: 35,
	},
	{
		// Strip tags first, then unescape. Unescaping first would turn this
		// escaped markup into real tags and delete the text.
		name:  "escaped markup is content, not tags",
		title: "Escaped",
		tags:  "",
		body:  "&lt;b&gt;not a tag&lt;/b&gt;",
		hash:  "a758e80e1119052a28e974bd8d1ad0877967624298e8ac78749dbbc8a957cb9e",
		runes: 25,
	},
	{
		// Truncation counts runes. A byte-wise cut here would land mid-character
		// and produce a different hash than the ETL's character-wise one.
		name:  "long multi-byte body truncates by rune",
		title: "Long",
		tags:  "",
		body:  "<p>" + strings.Repeat("xé ", 4000) + "</p>",
		hash:  "3a05bd7c947bd63317524a41c5e75e8b5cf6b711ba6554d515f475067e93a949",
		runes: 5000,
	},
	{
		name:  "an entirely empty article",
		title: "",
		tags:  "",
		body:  "<p></p>",
		hash:  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		runes: 0,
	},
	{
		name:  "numeric and named entities in tags and body",
		title: "Entities",
		tags:  "a&amp;b",
		body:  "<p>&nbsp;&#39;quoted&#39;</p>",
		hash:  "e7da0a366df46c77542065f919a89e4267b30cc5030c5f206ca60b69a6a3b356",
		runes: 27,
	},
}

func TestBuildEmbeddingSourceMatchesETLGoldens(t *testing.T) {
	for _, tc := range embeddingSourceGoldens {
		t.Run(tc.name, func(t *testing.T) {
			source := BuildEmbeddingSource(7, tc.title, tc.tags, tc.body)
			if source.ArticleID != 7 {
				t.Errorf("ArticleID = %d, want 7", source.ArticleID)
			}
			if got := len([]rune(source.Text)); got != tc.runes {
				t.Errorf("text is %d runes, want %d: %q", got, tc.runes, source.Text)
			}
			if source.Hash != tc.hash {
				t.Errorf("hash = %s, want %s\ntext: %q", source.Hash, tc.hash, source.Text)
			}
		})
	}
}

// A body edit must change the hash, or the reconciler never notices the article
// needs re-embedding. A change confined to markup must not, or every cosmetic
// save queues pointless inference.
func TestBuildEmbeddingSourceRespondsToContentNotMarkup(t *testing.T) {
	base := BuildEmbeddingSource(1, "Headline", "tag", "<p>The council voted.</p>")

	if same := BuildEmbeddingSource(1, "Headline", "tag", "<div><span>The council voted.</span></div>"); same.Hash != base.Hash {
		t.Error("a markup-only change altered the hash; every reformat would queue a re-embed")
	}
	if changed := BuildEmbeddingSource(1, "Headline", "tag", "<p>The council rejected it.</p>"); changed.Hash == base.Hash {
		t.Error("a body edit left the hash unchanged; the vector would never be refreshed")
	}
	if changed := BuildEmbeddingSource(1, "New headline", "tag", "<p>The council voted.</p>"); changed.Hash == base.Hash {
		t.Error("a headline rewrite left the hash unchanged")
	}
}

func TestFormatVector(t *testing.T) {
	if got, want := FormatVector([]float32{1, -0.5, 0}), "[1.00000000,-0.50000000,0.00000000]"; got != want {
		t.Errorf("FormatVector = %s, want %s", got, want)
	}
	if got := FormatVector(nil); got != "[]" {
		t.Errorf("FormatVector(nil) = %s, want []", got)
	}
}

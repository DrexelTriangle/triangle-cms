"""Unit tests for scripts/build_legacy_redirects.py (no database needed).

  python -m unittest discover -s scripts/tests
"""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import build_legacy_redirects as blr

CATEGORIES = {
    1: blr.WPCategory(1, "uncategorized", 0),
    3: blr.WPCategory(3, "news", 0),
    5: blr.WPCategory(5, "sports", 0),
    9: blr.WPCategory(9, "academic-transformation", 3),
}


def post(**overrides):
    fields = {
        "id": 67825,
        "post_name": "golf%e2%80%99s-season-winds-down",
        "post_date": "2012-04-01 21:30:00",
        "post_date_gmt": "2012-04-02 01:30:00",
        "title": "Golf&#8217;s season winds down",
        "categories": [5],
        "primary_category": None,
    }
    fields.update(overrides)
    return blr.WPPost(**fields)


def article(id, slug, pub_date="2012-04-02 01:30:00", title="Golf’s season winds down", archived=False):
    return blr.CMSArticle(id, slug, pub_date, title, archived)


class PermalinkTests(unittest.TestCase):
    def test_child_category_includes_its_parents(self):
        self.assertEqual(blr.category_path(9, CATEGORIES), "news/academic-transformation")

    def test_lowest_term_id_wins_without_a_primary_category(self):
        self.assertEqual(blr.permalink_category(post(categories=[9, 5]), CATEGORIES, 1), 5)

    def test_yoast_primary_category_overrides_when_still_assigned(self):
        self.assertEqual(blr.permalink_category(post(categories=[5, 9], primary_category=9), CATEGORIES, 1), 9)

    def test_stale_primary_category_is_ignored(self):
        self.assertEqual(blr.permalink_category(post(categories=[5], primary_category=9), CATEGORIES, 1), 5)

    def test_uncategorized_post_falls_back_to_the_default_category(self):
        self.assertEqual(blr.permalink_category(post(categories=[]), CATEGORIES, 1), 1)

    def test_category_structure(self):
        self.assertEqual(
            blr.permalink_path("/%category%/%postname%/", post(), "news/academic-transformation"),
            "/news/academic-transformation/golf%e2%80%99s-season-winds-down/",
        )

    def test_date_tags_use_local_time_not_gmt(self):
        # 21:30 local on the 1st is already the 2nd in GMT; WordPress dated the
        # URL by the local day.
        self.assertEqual(
            blr.permalink_path("/%year%/%monthnum%/%day%/%postname%/", post(), ""),
            "/2012/04/01/golf%e2%80%99s-season-winds-down/",
        )

    def test_unsupported_tag_fails_loudly(self):
        with self.assertRaises(blr.Fail):
            blr.permalink_path("/%author%/%postname%/", post(), "")


class MatchingTests(unittest.TestCase):
    def test_titles_compare_through_entities_and_curly_quotes(self):
        self.assertEqual(blr.normalize_title("Golf&#8217;s  season&amp;more"), blr.normalize_title("Golf’s season&more"))

    def test_percent_encoded_slug_candidate(self):
        self.assertIn("golf-e2-80-99s-season-winds-down", blr.cms_slug_candidates("golf%e2%80%99s-season-winds-down"))

    def test_unique_publish_time_matches(self):
        matcher = blr.Matcher([article(7, "golf-e2-80-99s-season-winds-down")])
        found, how = matcher.match(post())
        self.assertEqual((found.id, how), (7, "date"))

    def test_shared_publish_time_is_broken_on_title(self):
        matcher = blr.Matcher([
            article(7, "golf-e2-80-99s-season-winds-down"),
            article(8, "other-story", title="Something else entirely"),
        ])
        found, how = matcher.match(post())
        self.assertEqual((found.id, how), (7, "date+title"))

    def test_falls_back_to_the_rewritten_slug(self):
        matcher = blr.Matcher([article(7, "golf-e2-80-99s-season-winds-down", pub_date="2013-01-01 00:00:00")])
        found, how = matcher.match(post())
        self.assertEqual((found.id, how), (7, "slug"))

    def test_falls_back_to_a_unique_title_within_a_day(self):
        matcher = blr.Matcher([article(7, "ghost-world-7983", pub_date="2012-04-02 09:00:00")])
        found, how = matcher.match(post(post_name="ghost-world"))
        self.assertEqual((found.id, how), (7, "title"))

    def test_nothing_is_guessed(self):
        matcher = blr.Matcher([article(7, "unrelated", pub_date="2019-01-01 00:00:00", title="Unrelated")])
        found, how = matcher.match(post())
        self.assertIsNone(found)
        self.assertEqual(how, "unmatched")


class BuildTests(unittest.TestCase):
    def build(self, articles, manual=(), posts=None):
        return blr.build_redirects(
            posts or [post()], CATEGORIES, 1, "/%category%/%postname%/", blr.Matcher(articles), set(manual)
        )

    def test_three_rows_per_post(self):
        redirects, unplaced, _ = self.build([article(7, "golf-e2-80-99s-season-winds-down")])
        self.assertEqual(unplaced, [])
        self.assertEqual(
            {(r.source_kind, r.source, r.target, r.status) for r in redirects},
            {
                ("path", "/sports/golf%e2%80%99s-season-winds-down/", "/article/golf-e2-80-99s-season-winds-down", 301),
                ("post_id", "67825", "/article/golf-e2-80-99s-season-winds-down", 301),
                ("slug", "golf%e2%80%99s-season-winds-down", "/article/golf-e2-80-99s-season-winds-down", 301),
            },
        )

    def test_archived_article_is_gone(self):
        redirects, _, stats = self.build([article(7, "golf-e2-80-99s-season-winds-down", archived=True)])
        self.assertTrue(all(r.status == 410 and r.target == "" for r in redirects))
        self.assertEqual(stats["archived"], 1)

    def test_manual_rows_win(self):
        redirects, _, stats = self.build(
            [article(7, "golf-e2-80-99s-season-winds-down")], manual={("post_id", "67825")}
        )
        self.assertNotIn("post_id", {r.source_kind for r in redirects})
        self.assertEqual(stats["shadowed_by_manual"], 1)

    def test_post_named_after_a_category_gets_no_slug_row(self):
        redirects, _, stats = self.build(
            [article(7, "sports")], posts=[post(post_name="sports")]
        )
        self.assertEqual({r.source_kind for r in redirects}, {"path", "post_id"})
        self.assertEqual(stats["slug_is_category"], 1)

    def test_unmatched_post_is_reported_not_redirected(self):
        redirects, unplaced, _ = self.build([])
        self.assertEqual(redirects, [])
        self.assertEqual(len(unplaced), 1)

    def test_apply_replaces_only_generated_rows(self):
        redirects, _, _ = self.build([article(7, "golf-e2-80-99s-season-winds-down")])
        statements = blr.apply_statements(redirects)
        self.assertEqual(statements[0], "DELETE FROM `legacy_redirects` WHERE `origin` = 'wp_post';")
        self.assertEqual(len(statements), 2)
        # Values travel as hex, so nothing in a slug or title can break out.
        self.assertNotIn("golf", statements[1])


if __name__ == "__main__":
    unittest.main()

"""Tests for hack/release.py. Run: python3 -m unittest discover -s hack"""

import unittest

from release import Entry, Kind, classify, next_version, render


class ClassifyTest(unittest.TestCase):
    def test_cases(self):
        cases = [
            ("feature", "feat: add thing", "", Entry(Kind.FEATURE, "add thing")),
            ("scoped fix", "fix(helm): quote value", "", Entry(Kind.FIX, "quote value", scope="helm")),
            ("bang is breaking", "refactor!: rename", "", Entry(Kind.BREAKING, "rename")),
            ("scoped bang", "feat(api)!: drop v1", "", Entry(Kind.BREAKING, "drop v1", scope="api")),
            ("footer is breaking", "feat: new flag", "Details.\n\nBREAKING CHANGE: old flag\nremoved.\n\nCo-Authored-By: x",
             Entry(Kind.BREAKING, "new flag", note="old flag removed.")),
            ("hyphen footer", "fix: x", "BREAKING-CHANGE: y", Entry(Kind.BREAKING, "x", note="y")),
            ("other type is a chore, subject kept", "build(docker): add image", "",
             Entry(Kind.CHORE, "build(docker): add image")),
            ("not conventional", "Update README", "", Entry(Kind.CHORE, "Update README")),
        ]
        for name, subject, body, want in cases:
            with self.subTest(name):
                want = Entry(want.kind, want.desc, "abc1234", want.scope, want.note)
                self.assertEqual(classify("abc1234", subject, body), want)


class NextVersionTest(unittest.TestCase):
    def test_bumps(self):
        prev = (1, 4, 2)
        cases = [
            ("only fixes bump the patch", [Kind.FIX, Kind.FIX, Kind.CHORE], (1, 4, 3)),
            ("a feature bumps the minor", [Kind.FIX, Kind.FEATURE, Kind.CHORE], (1, 5, 0)),
            ("a breaking change bumps the major", [Kind.FEATURE, Kind.BREAKING], (2, 0, 0)),
            ("chores alone do not release", [Kind.CHORE, Kind.CHORE], None),
            ("no commits do not release", [], None),
        ]
        for name, kinds, want in cases:
            with self.subTest(name):
                self.assertEqual(next_version(prev, [Entry(k, "x") for k in kinds]), want)

    def test_first_release_is_1_0_0(self):
        for kind in (Kind.FIX, Kind.FEATURE, Kind.BREAKING):
            with self.subTest(kind.name):
                self.assertEqual(next_version(None, [Entry(kind, "x")]), (1, 0, 0))
        self.assertIsNone(next_version(None, [Entry(Kind.CHORE, "x")]))


class RenderTest(unittest.TestCase):
    def test_sections_in_order(self):
        entries = [
            Entry(Kind.CHORE, "tidy", "c1"),
            Entry(Kind.FIX, "quote", "f1", scope="helm"),
            Entry(Kind.BREAKING, "rename", "b1", note="annotations renamed"),
            Entry(Kind.FEATURE, "schedule", "a1"),
        ]
        notes = render(entries, "https://github.com/o/r/compare/v1.0.0...v2.0.0")
        order = ["## Breaking changes", "- rename (b1)\n  annotations renamed", "## Features",
                 "## Fixes", "- **helm:** quote (f1)", "## Chores", "**Full changelog:**"]
        positions = [notes.find(s) for s in order]
        self.assertNotIn(-1, positions, notes)
        self.assertEqual(positions, sorted(positions), notes)

    def test_empty_sections_and_link_omitted(self):
        notes = render([Entry(Kind.FIX, "x", "h")])
        self.assertNotIn("Features", notes)
        self.assertNotIn("Full changelog", notes)


if __name__ == "__main__":
    unittest.main()

"""Layer B conformance vectors, run against **this** parser.

`PORT_SPEC.md` §7.1: "The Layer B conformance vectors (§9) run against both
implementations in the same CI job and are what keeps them honest." This module
is the client's half of that, wired into the ordinary unit-test run so
``pytest python/tests`` (what `.github/workflows/golden.yml` invokes) covers it.

The vectors and the serialiser are **not** reimplemented here: the corpus is
``labfile/testdata/vectors`` and the serialiser is
``tools/vectorcheck/check_python.py``, the same module the v3.8.3 authority run
uses. Only the `Kathara` import resolution differs, which is the entire point.

``tools/vectorcheck/check_client.py`` is the standalone form of this, for
running the corpus by hand with a `--filter`.
"""

import os
import sys
import unittest

import _support

#: The repo root, found from this file: python/tests -> python -> <repo>.
REPO_ROOT = os.path.dirname(_support.PACKAGE_ROOT)
VECTORS = os.path.join(REPO_ROOT, "labfile", "testdata", "vectors")
VECTORCHECK = os.path.join(REPO_ROOT, "tools", "vectorcheck")

#: `labfile/testdata/vectors/README.md`: "142 vectors: 71 success, 71 error."
EXPECTED_VECTOR_COUNT = 142

HAVE_CORPUS = os.path.isdir(VECTORS) and os.path.isfile(os.path.join(VECTORCHECK, "check_python.py"))


def _load_runner():
    """Import the shared vector runner, with `Kathara` bound to the client."""
    if VECTORCHECK not in sys.path:
        sys.path.insert(0, VECTORCHECK)

    import check_python

    return check_python


@unittest.skipUnless(HAVE_CORPUS, "vector corpus not present (installed package, not a checkout)")
class VectorConformanceTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.runner = _load_runner()

        import Kathara

        resolved = os.path.abspath(os.path.dirname(Kathara.__file__))
        expected = os.path.join(_support.PACKAGE_ROOT, "Kathara")
        if resolved != expected:
            # Not a skip: a misconfigured environment that silently drops the
            # whole Layer B suite is exactly the failure the merge gate ("zero
            # tests skipped or deleted", `PORT_SPEC.md` §11) is written against.
            raise AssertionError(
                "Kathara resolves to %s, not the client at %s; the run would not test this package"
                % (resolved, expected)
            )

        cls.vector_dirs = cls.runner.discover(VECTORS)

    def test_corpus_is_complete(self):
        self.assertEqual(EXPECTED_VECTOR_COUNT, len(self.vector_dirs))

    def test_every_vector_matches(self):
        import json

        failures = []

        for vector_dir in self.vector_dirs:
            vector_id = os.path.relpath(vector_dir, VECTORS)

            with open(os.path.join(vector_dir, "vector.json")) as handle:
                vector = json.load(handle)
            with open(os.path.join(vector_dir, "expected.json")) as handle:
                expected = json.load(handle)

            actual = self.runner.run_vector(vector_dir, vector)
            order_sensitive = vector.get("order_sensitive", True)

            normalized_expected = self.runner.normalize(expected, order_sensitive)
            normalized_actual = self.runner.normalize(actual, order_sensitive)

            with self.subTest(vector=vector_id):
                if normalized_expected != normalized_actual:
                    failures.append(vector_id)
                self.assertEqual(
                    normalized_expected,
                    normalized_actual,
                    "\n".join(self.runner.diff_lines(normalized_expected, normalized_actual)),
                )

        self.assertEqual([], failures)


if __name__ == "__main__":
    unittest.main()

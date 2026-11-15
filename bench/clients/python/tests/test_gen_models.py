"""scripts/gen_models.py turns OpenAPI components into importable TypedDicts."""

import contextlib
import importlib.util
import io
import json
import os
import sys
import tempfile
import types
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
spec = importlib.util.spec_from_file_location("gen_models", os.path.join(HERE, "..", "scripts", "gen_models.py"))
gen_models = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gen_models)

DOC = {
    "paths": {
        "/api/bench/things/{id}": {"get": {"responses": {
            "200": {"content": {"application/json": {"schema": {"allOf": [
                {"$ref": "#/components/schemas/Thing"}, {"$ref": "#/components/schemas/ModeField"}]}}}},
        }}},
        "/api/bench/catalog": {"get": {"responses": {
            "200": {"content": {"application/json": {"schema": {"allOf": [
                {"$ref": "#/components/schemas/Catalog"}, {"$ref": "#/components/schemas/ModeField"}]}}}},
        }}},
    },
    "components": {"schemas": {
        "ModeField": {"type": "object", "properties": {"mode": {"type": "string"}}, "required": ["mode"]},
        "Thing": {"type": "object", "required": ["id", "tags"], "properties": {
            "id": {"type": "string"},
            "tags": {"type": "array", "items": {"type": "string"}},
            "parent": {"anyOf": [{"$ref": "#/components/schemas/Thing"}, {"type": "null"}]},
            "scores": {"type": "object", "additionalProperties": {"type": "number"}},
            "list": {"$ref": "#/components/schemas/List"},
        }},
        "List": {"type": "object", "properties": {"n": {"type": "integer"}}},
        "Span": {"type": "object", "required": ["to"], "properties": {"from": {"type": "integer"}, "to": {"type": "integer"}}},
        "Catalog": {"type": "object", "required": ["mode"], "properties": {"mode": {"type": "string"}}},
    }},
}


def load(source):
    mod = types.ModuleType("generated_models")
    sys.modules[mod.__name__] = mod
    exec(compile(source, "generated_models.py", "exec"), mod.__dict__)
    return mod


class GenModelsTest(unittest.TestCase):
    def test_generates_typed_dicts(self) -> None:
        mod = load(gen_models.generate(DOC))
        self.assertEqual(mod.Thing.__required_keys__, frozenset({"id", "tags"}))
        self.assertEqual(mod.Thing.__optional_keys__, frozenset({"parent", "scores", "list"}))
        self.assertIn("mode", mod.ThingResponse.__required_keys__)
        self.assertIs(mod.CatalogResponse, mod.Catalog)  # carries its own mode
        self.assertTrue(hasattr(mod, "List_"))  # a reserved name gets a suffix
        self.assertEqual(mod.Span.__optional_keys__, frozenset({"from", "to"}))  # keyword key: functional form
        self.assertFalse(hasattr(mod, "ModeField"))
        for name in mod.__all__:
            self.assertTrue(hasattr(mod, name), name)

    def test_annotations(self) -> None:
        src = gen_models.generate(DOC)
        self.assertIn("parent: Optional[Thing]", src)
        self.assertIn("scores: Dict[str, float]", src)
        self.assertIn("tags: List[str]", src)
        self.assertIn("list: List_", src)

    def test_check_mode(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            out, doc_path = os.path.join(tmp, "models.py"), os.path.join(tmp, "openapi.json")
            with open(doc_path, "w", encoding="utf-8") as fh:
                json.dump(DOC, fh)
            self.assertEqual(gen_models.main(["--file", doc_path, "--out", out]), 0)
            self.assertEqual(gen_models.main(["--file", doc_path, "--check", "--out", out]), 0)
            with open(out, "a", encoding="utf-8") as fh:
                fh.write("# edited\n")
            with contextlib.redirect_stderr(io.StringIO()) as err:
                self.assertEqual(gen_models.main(["--file", doc_path, "--check", "--out", out]), 1)
            self.assertIn("stale", err.getvalue())


if __name__ == "__main__":
    unittest.main()

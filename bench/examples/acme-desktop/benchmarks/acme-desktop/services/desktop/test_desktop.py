"""Unit tests of desktop.py that need no display: input validation, the
XWD to PNG path, the JSON-RPC layer, the form's submit logic and the state
dump. Run them in the image (or anywhere with python3):

  docker run --rm <image> python3 -m unittest discover -s /opt/desktop -v
"""

import json
import os
import struct
import sys
import tempfile
import unittest
import zlib

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import desktop  # noqa: E402


def make_xwd(width, height, pixel, byte_order=0, endian=">"):
    """A 32 bpp ZPixmap XWD image filled with one (r, g, b) colour."""
    name = b"test\x00"
    fields = [100 + len(name), 7, 2, 24, width, height, 0, byte_order, 32, 0, 32, 32, width * 4,
              4, 0xFF0000, 0x00FF00, 0x0000FF, 8, 0, 0, width, height, 0, 0, 0]
    r, g, b = pixel
    px = bytes([b, g, r, 0]) if byte_order == 0 else bytes([0, r, g, b])
    return struct.pack(endian + "25I", *fields) + name + px * (width * height)


def png_size(png):
    assert png[:8] == b"\x89PNG\r\n\x1a\n"
    return struct.unpack(">II", png[16:24])


def png_pixels(png):
    w, h = png_size(png)
    pos, data = 8, b""
    while pos < len(png):
        n = struct.unpack(">I", png[pos:pos + 4])[0]
        kind = png[pos + 4:pos + 8]
        if kind == b"IDAT":
            data += png[pos + 8:pos + 8 + n]
        pos += 12 + n
    raw = zlib.decompress(data)
    stride = w * 3 + 1
    return [raw[y * stride + 1:(y + 1) * stride] for y in range(h)]


class ComboTest(unittest.TestCase):
    def test_valid(self):
        cases = {"ctrl+s": "ctrl+s", "Return": "Return", "enter": "Return", "alt+F4": "alt+F4",
                 "ctrl+shift+Home": "ctrl+shift+Home", "Control+a": "ctrl+a", "esc": "Escape", "7": "7",
                 "shift+Tab": "shift+Tab", "page_down": "Page_Down"}
        for combo, want in cases.items():
            with self.subTest(combo=combo):
                self.assertEqual(desktop.parse_combo(combo), want)

    def test_invalid(self):
        for combo in ["", "ctrl+", "ctrl s", "a+b", "rm -rf", "ctrl+alt+shift+super+x", "ctrl+;", "foo", 5, None, "ctrl+shift"]:
            with self.subTest(combo=combo), self.assertRaises(desktop.ToolError):
                desktop.parse_combo(combo)


class TextTest(unittest.TestCase):
    def test_valid(self):
        for text in ["hello", "a\nb\tc", "Zoë 86,40 €"]:
            self.assertEqual(desktop.check_text(text), text)

    def test_invalid(self):
        for text in ["", None, 3, "a\x1bb", "x" * (desktop.MAX_TEXT + 1), "bell\x07"]:
            with self.subTest(text=repr(text)[:20]), self.assertRaises(desktop.ToolError):
                desktop.check_text(text)


class ArgsTest(unittest.TestCase):
    def test_point_bounds(self):
        size = (1280, 800)
        self.assertEqual(desktop.point({"x": 0, "y": 799}, size), (0, 799))
        self.assertEqual(desktop.point({"x": 10.0, "y": 5}, size), (10, 5))
        for args in [{"x": 1280, "y": 0}, {"x": -1, "y": 0}, {"x": 0, "y": 800}, {"x": "5", "y": 1}, {"x": True, "y": 1}, {"x": 1.5, "y": 1}, {"y": 1}]:
            with self.subTest(args=args), self.assertRaises(desktop.ToolError):
                desktop.point(args, size)

    def test_call_tool_rejects_before_input(self):
        click = next(t for t in desktop.TOOLS if t["name"] == "click")
        for args in [{"x": 5}, {"x": 5, "y": 5, "shell": "id"}, {"x": 5000, "y": 5}, {"x": 5, "y": 5, "button": "fourth"}, {"x": 5, "y": 5, "count": 4}]:
            with self.subTest(args=args), self.assertRaises(desktop.ToolError):
                desktop.call_tool(click, args)


class ScreenshotTest(unittest.TestCase):
    def test_xwd_to_png(self):
        for byte_order in (0, 1):
            for endian in (">", "<"):
                with self.subTest(byte_order=byte_order, endian=endian):
                    w, h, rows = desktop.read_xwd(make_xwd(8, 4, (10, 20, 30), byte_order, endian))
                    self.assertEqual((w, h), (8, 4))
                    png = desktop.encode_png(w, h, rows)
                    self.assertEqual(png_size(png), (8, 4))
                    self.assertEqual(png_pixels(png)[0][:3], bytes([10, 20, 30]))

    def test_downscale(self):
        w, h, rows = desktop.read_xwd(make_xwd(9, 5, (1, 2, 3)))
        sw, sh, small = desktop.downscale(w, h, rows, 2)
        self.assertEqual((sw, sh), (5, 3))
        self.assertTrue(all(len(r) == 15 for r in small))
        self.assertEqual(desktop.downscale(w, h, rows, 1)[:2], (9, 5))

    def test_bad_xwd(self):
        for data in [b"", b"x" * 100, make_xwd(4, 4, (0, 0, 0))[:150]]:
            with self.assertRaises(ValueError):
                desktop.read_xwd(data)


class RPCTest(unittest.TestCase):
    def call(self, method, params=None, msg_id=1):
        return desktop.handle_message({"jsonrpc": "2.0", "id": msg_id, "method": method, "params": params or {}})

    def test_initialize_and_list(self):
        init = self.call("initialize", {"protocolVersion": "2025-03-26"})["result"]
        self.assertEqual(init["protocolVersion"], "2025-03-26")
        self.assertIn("tools", init["capabilities"])
        names = [t["name"] for t in self.call("tools/list")["result"]["tools"]]
        self.assertEqual(names, ["screenshot", "click", "move", "drag", "type", "key", "scroll", "wait", "cursor_position", "screen_size"])
        for forbidden in ("shell", "exec", "run", "read_file", "bash"):
            self.assertNotIn(forbidden, names)

    def test_errors(self):
        self.assertEqual(self.call("tools/call", {"name": "shell"})["error"]["code"], -32602)
        self.assertEqual(self.call("nope")["error"]["code"], -32601)
        self.assertIsNone(desktop.handle_message({"jsonrpc": "2.0", "method": "notifications/initialized"}))
        self.assertEqual(desktop.handle_message({"id": 1})["error"]["code"], -32600)
        bad = self.call("tools/call", {"name": "key", "arguments": {"combo": "ctrl+;"}})["result"]
        self.assertTrue(bad["isError"])
        out = self.call("tools/call", {"name": "click", "arguments": {"x": 99999, "y": 1}})["result"]
        self.assertTrue(out["isError"])
        self.assertIn("between", out["content"][0]["text"])

    def test_screen_size_and_wait(self):
        os.environ["DESKTOP_SIZE"] = "1024x768"
        try:
            out = self.call("tools/call", {"name": "screen_size", "arguments": {}})["result"]
            self.assertEqual(json.loads(out["content"][0]["text"]), {"height": 768, "width": 1024})
        finally:
            del os.environ["DESKTOP_SIZE"]
        out = self.call("tools/call", {"name": "wait", "arguments": {"ms": 1}})["result"]
        self.assertFalse(out["isError"])
        self.assertTrue(self.call("tools/call", {"name": "wait", "arguments": {"ms": 60000}})["result"]["isError"])


class FormTest(unittest.TestCase):
    SPEC = {"title": "T", "fields": [
        {"id": "name", "label": "Name", "required": True},
        {"id": "cat", "label": "Category", "kind": "choice", "options": ["Travel", "Meals"], "required": True},
        {"id": "ok", "label": "OK", "kind": "check"}]}

    def test_validate(self):
        self.assertEqual(desktop.validate_form(self.SPEC, {"name": "A", "cat": "Travel", "ok": False}), "")
        self.assertIn("required", desktop.validate_form(self.SPEC, {"name": "", "cat": "Travel", "ok": True}))
        self.assertIn("one of", desktop.validate_form(self.SPEC, {"name": "A", "cat": "Fun", "ok": True}))

    def test_submit_appends(self):
        with tempfile.TemporaryDirectory() as d:
            spec = dict(self.SPEC, output=os.path.join(d, "out", "s.json"))
            self.assertEqual(desktop.submit_form(spec, {"name": "A"}), 1)
            self.assertEqual(desktop.submit_form(spec, {"name": "B"}), 2)
            with open(spec["output"]) as f:
                doc = json.load(f)
            self.assertEqual([s["values"]["name"] for s in doc["submissions"]], ["A", "B"])


class DumpTest(unittest.TestCase):
    def test_walk_and_ini(self):
        with tempfile.TemporaryDirectory() as d:
            os.makedirs(os.path.join(d, "Inbox"))
            os.makedirs(os.path.join(d, "Empty"))
            with open(os.path.join(d, "app.conf"), "w") as f:
                f.write("name = demo\n[server]\nport = 8080\n")
            with open(os.path.join(d, "Inbox", "a.bin"), "wb") as f:
                f.write(b"\xff\xfe")
            paths, dirs, tree = desktop.walk(d)
            self.assertEqual(paths, ["Inbox/a.bin", "app.conf"])
            self.assertEqual(dirs, ["Empty", "Inbox"])
            self.assertEqual(tree["Empty"], {})
            self.assertEqual(tree["app.conf"]["ini"], {"top": {"name": "demo"}, "server": {"port": "8080"}})
            self.assertNotIn("text", tree["Inbox"]["a.bin"])
            self.assertEqual(tree["Inbox"]["a.bin"]["bytes"], 2)

    def test_seed_home(self):
        with tempfile.TemporaryDirectory() as seed, tempfile.TemporaryDirectory() as home:
            os.makedirs(os.path.join(seed, "Documents", "Inbox"))
            with open(os.path.join(seed, "Documents", "Inbox", "a.txt"), "w") as f:
                f.write("a")
            old = dict(os.environ)
            os.environ.update(DESKTOP_SEED=seed, HOME=home)
            try:
                desktop.seed_home()
            finally:
                os.environ.clear()
                os.environ.update(old)
            with open(os.path.join(home, "Documents", "Inbox", "a.txt")) as f:
                self.assertEqual(f.read(), "a")

    def test_parse_ini_error(self):
        self.assertIn("error", desktop.parse_ini("[a\nb"))


if __name__ == "__main__":
    unittest.main()

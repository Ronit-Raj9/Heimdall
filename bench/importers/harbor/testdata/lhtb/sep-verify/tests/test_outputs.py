from pathlib import Path


def test_hello_file_content():
    assert Path("/app/hello.txt").read_text() == "hello, world\n"

#!/usr/bin/env python3
"""Count the o200k_base tokens of long-context request bodies.

    uv run --no-project --with tiktoken python tokens.py W1t-*.json

For each body it prints its size in bytes, the tokens of its text (every
message's content, and each tool call's name and arguments) and the tokens
of the whole body as JSON text.
"""

import json
import sys

import tiktoken


def main():
    enc = tiktoken.get_encoding("o200k_base")
    count = lambda s: len(enc.encode(s, disallowed_special=()))
    for path in sys.argv[1:]:
        raw = open(path, "rb").read()
        req = json.loads(raw)
        text = 0
        for m in req["messages"]:
            content = m.get("content")
            if isinstance(content, str):
                text += count(content)
            elif isinstance(content, list):
                text += sum(count(p.get("text", "")) for p in content)
            for c in m.get("tool_calls") or []:
                text += count(c["function"]["name"]) + count(c["function"]["arguments"])
        print(f"{path}: {len(raw)} bytes, {len(req['messages'])} messages, "
              f"{text} text tokens, {count(raw.decode())} body tokens")


if __name__ == "__main__":
    main()

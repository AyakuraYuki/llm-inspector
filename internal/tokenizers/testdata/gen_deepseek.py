import json, sys
from transformers import PreTrainedTokenizerFast

D = "../../configs/tokenizers/deepseek-v4-flash-0731"
tk = PreTrainedTokenizerFast(tokenizer_file=D + "/tokenizer.json")

CASES = [
    "",
    "a",
    " ",
    "  ",
    "\n",
    "Just say hi to everyone.",
    "The quick brown fox jumps over the lazy dog.",
    "你好，世界！",
    "你好，世界！这是一个中文测试。",
    "敏捷的棕色狐狸跳过了懒狗",
    "混合 mixed 文本 text 123 456 测试",
    "1234567890",
    "3.14159265358979",
    "def foo(x):\n    return x + 1\n",
    "```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```",
    "SELECT * FROM users WHERE id = 42 AND name LIKE '%abc%';",
    "emoji: 🎉🚀✨ and 汉字 and ﾊﾝｶｸ",
    "trailing spaces   ",
    "   leading spaces",
    "tabs\tand\tnewlines\n\n\nmultiple",
    "日本語のテキストです。カタカナもひらがなも。",
    "한국어 텍스트",
    "Ĥéllo Wörld naïve café",
    "<｜begin▁of▁sentence｜>hello<｜end▁of▁sentence｜>",
    "a<｜▁pad▁｜>b",
    "x" * 200,
    "中" * 100,
    "!@#$%^&*()_+-=[]{}|;':\",./<>?",
    "line1\r\nline2\r\n",
    "  　 unicode spaces",
    "MixedCASE camelCase snake_case SCREAMING_SNAKE",
    "url: https://example.com/path?a=1&b=2#frag",
]

out = []
for s in CASES:
    ids = tk.encode(s, add_special_tokens=False)
    out.append({"text": s, "ids": ids, "count": len(ids)})

json.dump(out, open("/tmp/tkgold/deepseek.golden.json", "w"), ensure_ascii=False, indent=1)
print("cases:", len(out), "| total tokens:", sum(c["count"] for c in out))
for c in out[:8]:
    print(f'{c["count"]:4d}  {c["text"]!r}')

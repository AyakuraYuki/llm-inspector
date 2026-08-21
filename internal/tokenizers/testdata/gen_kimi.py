import base64, json
import tiktoken

D = "../../configs/tokenizers/kimi-k3"

pat_str = "|".join([
    r"""[\p{Han}]+""",
    r"""[^\r\n\p{L}\p{N}]?[\p{Lu}\p{Lt}\p{Lm}\p{Lo}\p{M}&&[^\p{Han}]]*[\p{Ll}\p{Lm}\p{Lo}\p{M}&&[^\p{Han}]]+(?i:'s|'t|'re|'ve|'m|'ll|'d)?""",
    r"""[^\r\n\p{L}\p{N}]?[\p{Lu}\p{Lt}\p{Lm}\p{Lo}\p{M}&&[^\p{Han}]]+[\p{Ll}\p{Lm}\p{Lo}\p{M}&&[^\p{Han}]]*(?i:'s|'t|'re|'ve|'m|'ll|'d)?""",
    r"""\p{N}{1,3}""",
    r""" ?[^\s\p{L}\p{N}]+[\r\n]*""",
    r"""\s*[\r\n]+""",
    r"""\s+(?!\S)""",
    r"""\s+""",
])

ranks = {}
with open(D + "/tiktoken.model") as f:
    for line in f:
        tok, rank = line.split()
        ranks[base64.b64decode(tok)] = int(rank)

n_base = len(ranks)
cfg = json.load(open(D + "/tokenizer_config.json"))
mapping = {int(i): v["content"] for i, v in cfg["added_tokens_decoder"].items()}
special = {mapping.get(i, f"<|reserved_token_{i}|>"): i for i in range(n_base, n_base + 256)}

enc = tiktoken.Encoding(name="kimi-k3", pat_str=pat_str, mergeable_ranks=ranks, special_tokens=special)
print("n_vocab:", enc.n_vocab, "| base:", n_base)

CASES = json.load(open("/tmp/tkgold/deepseek.golden.json"))
out = []
for c in CASES:
    s = c["text"]
    ids = enc.encode(s, disallowed_special=())
    out.append({"text": s, "ids": ids, "count": len(ids)})
json.dump(out, open("/tmp/tkgold/kimi.golden.json", "w"), ensure_ascii=False, indent=1)
print("cases:", len(out), "| total tokens:", sum(c["count"] for c in out))
for c in out[5:9]:
    print(f'{c["count"]:4d}  {c["text"]!r}')

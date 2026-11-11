"""Write LICENSES/CRATES.md: the crates linked into why-hwp-engine and their licenses.

Usage (from wasm/):
  cargo metadata --locked --format-version 1 --filter-platform wasm32-wasip1 \
    | python3 list-crates.py > LICENSES/CRATES.md
"""
import json
import sys

meta = json.load(sys.stdin)
packages = {p["id"]: p for p in meta["packages"]}
nodes = {n["id"]: n for n in meta["resolve"]["nodes"]}
root = meta["resolve"]["root"]

# Follow normal (not build or dev) dependencies from the engine crate.
seen, stack = set(), [root]
while stack:
    node = stack.pop()
    if node in seen:
        continue
    seen.add(node)
    for dep in nodes[node]["deps"]:
        if any(kind["kind"] is None for kind in dep["dep_kinds"]):
            stack.append(dep["pkg"])

linked = sorted((packages[i] for i in seen if i != root), key=lambda p: (p["name"], p["version"]))
print("# Crates in why-hwp-engine\n")
print("Rust crates linked into `engine.wasm.gz` for wasm32-wasip1, from `wasm/Cargo.lock`.")
print("Regenerate with `wasm/list-crates.py` when the lock file changes.\n")
print("| Crate | Version | License | Repository |")
print("|-------|---------|---------|------------|")
for p in linked:
    source = "rhwp (vendored)" if p["source"] is None else (p.get("repository") or "")
    print(f"| {p['name']} | {p['version']} | {p['license'] or ''} | {source} |")

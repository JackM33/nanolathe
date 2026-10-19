#!/usr/bin/env python3
"""Derive the prototype curation recipe from the TA Archive map index.

Selection pools (user-approved priority order): author-curated packs where the
index identifies them, AI-ready maps, era classics, and — so the list stays
interesting — a spread of unusual world families (space, crystal, moon, mars,
metal, acid, water, lava, ice, rendered). The output is a reviewable recipe for
tools/map-curate; nothing is downloaded here.

The world family comes from FileUniverse's r2010 tileset folders as archived
by the Wayback Machine, matched to TA Archive titles by "name (r2010)".

Usage: tools/map-lab/select.py --out tools/map-lab/recipes/ta-archive-prototype.json
"""
import argparse
import html
import json
import pathlib
import re
import sys
import time
import urllib.parse
import urllib.request

INDEX = "https://ta-archive.com/maps/"
WAYBACK = "https://web.archive.org/web/2023/https://files.tauniverse.com/files/ta/maps-2/{folder}/"
USER_AGENT = "nanolathe-map-lab/0.1 (+https://nanolathe.gg)"

# world family -> (wayback folder, take)
FAMILIES = {
    "space": ("space", 20),
    "crystal": ("crystal", 20),
    "moon": ("moon-and-luna", 12),
    "mars": ("mars", 12),
    "metal": ("metal-(metal-worlds)", 30),
    "acid": ("acid", 10),
    "water": ("water", 10),
    "lava": ("lava", 10),
    "ice": ("ice", 10),
    "rendered": ("rendered-and-other-worlds", 25),
    "green": ("green", 60),
}

# Well-known classic multiplayer names; used only to prefer one green map over
# another when otherwise equal.
CLASSICS = [
    "Sherwood", "Painted Desert", "The Cold Place", "Greenhaven", "Ror Shock", "Flame Field",
    "Dark Side", "Great Divide", "The Pass", "Red River", "Lava Highground", "Ice Scream",
    "Over Crude Water", "John's Pass", "A Better Place To Be", "Coasts to Coasts",
    "The Bayou", "Zulu", "Anteer Straight", "Craters", "Ice Berg", "Dragon's Teeth",
    "The Divided", "Two Continents", "Yerrot Mountains", "Shore to Shore", "The Rock",
    "Not a Pic-Nic", "Supreme Conflict",
]


def fetch(url):
    request = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
    with urllib.request.urlopen(request, timeout=120) as response:
        return response.read().decode("utf-8", "replace")


def max_players(text):
    numbers = re.findall(r"\d+", text or "")
    return max(int(number) for number in numbers) if numbers else 0


def parse_cards(page):
    records = []
    for match in re.finditer(r'class="map-card', page):
        start = match.start()
        tag = page[start:page.find(">", start)]
        attrs = {}
        for key, quoted, bare in re.findall(r'([a-z-]+)=(?:"([^"]*)"|([^\s>]+))', tag):
            attrs[key] = quoted or bare
        flat = {k: html.unescape(v) for k, v in attrs.items()}
        title = flat.get("data-title", "").strip()
        if not title:
            continue
        records.append({
            "title": title,
            "zip": urllib.parse.urljoin(INDEX, flat.get("data-zip", "")),
            "page": urllib.parse.urljoin(INDEX, flat.get("data-page", "")),
            "players": max_players(flat.get("data-players")),
            "sizemap": flat.get("data-sizemap", ""),
            "mb": float(flat.get("data-mb") or 0),
            "year": int(flat.get("data-year") or 0),
            "author": flat.get("data-author", "").strip(),
            "ai": flat.get("data-aiprofile-name", "").strip(),
            "aistatus": flat.get("data-aiprofile-status", "").strip(),
            "desc": (flat.get("data-otadesc") or flat.get("data-desc") or flat.get("data-otabrief") or "").strip(),
        })
    return records


CACHE = pathlib.Path(__file__).resolve().parent / "cache"


def folder_names(folder):
    """The r2010 folder's map names, from the reviewed cache when present.

    The cache makes the recipe deterministic; --refresh refetches and rewrites
    it. Folder captures are FileUniverse snapshots, so a refetch can change
    the pools — the recipe, not the generator, is the curated artifact."""
    cached = CACHE / (folder + ".json")
    if cached.exists():
        return {name.casefold() for name in json.loads(cached.read_text())}
    last = None
    for attempt in range(4):
        try:
            page = fetch(WAYBACK.format(folder=urllib.parse.quote(folder, safe="()")))
        except Exception as error:
            last = error
            time.sleep(3 + 3 * attempt)
            continue
        names = set()
        for href in re.findall(r'href="([^"?]+\.zip)"', page):
            name = urllib.parse.unquote(pathlib.PurePosixPath(href).name)
            name = re.sub(r"\s*\(r2010\)\.zip$", "", name, flags=re.I).strip()
            if name:
                names.add(name)
        if names:
            cached.parent.mkdir(parents=True, exist_ok=True)
            cached.write_text(json.dumps(sorted(names), indent=1) + "\n")
            return {name.casefold() for name in names}
    raise last if last else RuntimeError(f"no names in {folder}")


def slug(title):
    text = re.sub(r"[^a-z0-9]+", "-", title.lower()).strip("-")
    return re.sub(r"-{2,}", "-", text)[:64].strip("-") or "map"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", required=True, type=pathlib.Path)
    parser.add_argument("--limit", type=int, default=260)
    args = parser.parse_args()

    records = parse_cards(fetch(INDEX))
    print(f"{len(records)} maps in the index", file=sys.stderr)
    by_fold = {}
    for record in records:
        by_fold.setdefault(record["title"].casefold(), record)

    chosen, ids, seen_titles, per_author = [], set(), set(), {}
    kinds_by_name = {}

    def add(record, family, why):
        key = record["title"].casefold()
        if key in seen_titles or record["players"] < 2 or record["mb"] > 8:
            return False
        author = record["author"].casefold()
        if author and per_author.get(author, 0) >= 12:
            return False
        ident = slug(record["title"])
        if ident in ids:
            ident = f"{ident}-{len(ids)}"
        ids.add(ident)
        seen_titles.add(key)
        if author:
            per_author[author] = per_author.get(author, 0) + 1
        entry = {"id": ident, "title": record["title"], "url": record["zip"],
                 "page": record["page"], "kind": family, "why": why}
        for key_in in ("author", "players", "sizemap", "year"):
            if record[key_in]:
                entry[key_in] = record[key_in]
        if record["desc"]:
            entry["desc"] = record["desc"]
        marker = record["ai"] or ("yes" if record["aistatus"] else "")
        if marker:
            entry["ai"] = marker
        chosen.append(entry)
        return True

    for family, (folder, take) in FAMILIES.items():
        try:
            names = folder_names(folder)
        except Exception as error:  # a missing capture is not fatal
            print(f"{family}: folder listing unavailable ({error})", file=sys.stderr)
            continue
        for name in names:
            kinds_by_name.setdefault(name, family)
        pool = [by_fold[n] for n in names if n in by_fold]
        if family == "green":
            pool.sort(key=lambda r: (not r["author"],
                                     not any(c.casefold() in r["title"].casefold() for c in CLASSICS),
                                     -r["players"], -r["year"]))
        else:
            pool.sort(key=lambda r: (not r["author"], -r["players"], -r["year"]))
        picked = 0
        for record in pool:
            if picked >= take:
                break
            if add(record, family, "pool"):
                picked += 1
        print(f"{family}: {picked}/{take}", file=sys.stderr)

    # Fill to the limit with AI-ready maps from anywhere.
    for record in sorted(records, key=lambda r: (-r["players"], -r["year"])):
        if len(chosen) >= args.limit:
            break
        if record["ai"] or record["aistatus"]:
            add(record, kinds_by_name.get(record["title"].casefold(), "mixed"), "ai-ready")

    args.out.parent.mkdir(parents=True, exist_ok=True)
    recipe = {"schema": 1, "version": "r2010", "maps": chosen, "dependencies": []}
    args.out.write_text(json.dumps(recipe, indent=2) + "\n")
    print(f"{len(chosen)} maps -> {args.out}", file=sys.stderr)


if __name__ == "__main__":
    main()

# The map lab

A local, reviewable prototype of the curated community map set: recipe ->
download -> expand -> package -> preview -> verify, served to the desktop
client's **More maps** dialog via `NANOLATHE_MAP_CATALOG`
(docs/DESIGN_MODS_MUTATORS.md §5.6).

Nothing here is published. Map bytes and built packages live outside the
repository (default `~/nanolathe-map-lab`); this directory holds only the
recipe, the tileset-folder name cache that makes recipe generation
deterministic, and the serving helper.

## Pipeline

```sh
# 1. Build (or rebuild) the lab. --verify runs the engine's map admission;
#    the two preparation steps resolve feature conflicts at packaging time.
python3 tools/map-curate \
  --recipe tools/map-lab/recipes/ta-archive-prototype.json \
  --lab ~/nanolathe-map-lab \
  --base "/path/to/Total Annihilation" \
  --promote-shared-features \
  --prune-base-features \
  --verify

# 2. Serve the lab; it prints the exact client command.
tools/map-lab/serve.sh
```

## Regenerating the recipe

```sh
python3 tools/map-lab/select.py --out tools/map-lab/recipes/ta-archive-prototype.json
```

`select.py` reads the live TA Archive index and the cached r2010 tileset
folders (`cache/<folder>.json`, collected once from the FileUniverse mirror).
Delete a cache file to refetch it. The recipe — not the generator — is the
reviewed artifact: prune refused maps there and rebuild.

## Review notes

- Selection pools, in order: author-curated packs, AI-ready maps, era
  classics, unusual world families (space, crystal, moon, mars, metal, acid,
  water, lava, ice, rendered).
- Every package keeps its source credits (`CREDITS.txt`) and the TA Archive
  page as its homepage; the catalogue records the author.
- Previews are annotated with the map's own features: yellow marks metal
  deposits, green reclaimable ground (`map-previews --annotate`). The browser
  draws the manifest's `legend` list above the picture as a coloured dot and
  its name.
- `map-verify` refuses a map the base install already supplies and a map whose
  features do not resolve; those entries are pruned from the recipe.
- Feature conflicts resolve at packaging: a feature several maps define
  differently moves into the dependency (`--promote-shared-features`), and a
  feature a map redefines against the base install is dropped
  (`--prune-base-features`); both steps print every change.
- Maps needing TAMEC/community feature records return when that feature pack
  is hosted as a catalogue dependency: TA Archive's `features-data.json`
  indexes the records (name, TDF path, downloadable GAF) but the TDF bodies
  live in the TAMEC2004 pack, whose ModDB download blocks scripted fetches.
  The lab's `community-features` dependency is built from the TAMEC2004 and
  TA Features 2013 archives, dropped into `incoming/`.

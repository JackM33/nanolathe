# Curated community map packages

## Evidence and scope

**Established — authored content, inspected October 5, 2026.** This reference
covers the first twenty maps and the ten-map second batch below, with exact
source artifacts in the package inventory. It establishes content identity
and dependency closure, not
retail mechanics, author permission beyond preserved notices, or complete
battle compatibility. The user authorized a separate on-demand map library;
its host policy is owned by [DESIGN_MODS_MUTATORS](../../docs/DESIGN_MODS_MUTATORS.md).
No retail maps or base-game archives belong to these packages.

Primary publisher references are the [TA multiplayer map downloads](https://tadg.tauniverse.com/downloads.htm),
[Greybeard's map page](https://thepack.tauniverse.com/Maps.shtml), and
[Wotan's Escalation map page](https://taesc.tauniverse.com/?p=maps).
The [April 17, 2025 archived Wotan page](https://web.archive.org/web/20250417185656/https://taesc.tauniverse.com/?p=maps)
retains the original download targets when the live publisher's Cloudflare
challenge prevents retrieval. [TA Archive](https://www.ta-archive.com/maps/)
provides preserved downloads with the authored OTA, TNT and accompanying
readmes. Archive listing labels alone do not establish a different edition.

**Established — package identity.** Package versions `2` and `5` identify
Metal Picnic V2 and Burning Woods V5. `original` is a package identity for an
unversioned upstream edition, not an invented author release number. ZIP roots
contain unchanged authored loose map and feature bytes plus identity-only
schema 1 `nanolathe-mod.json`, original documentation and `provenance.txt`.
HPI archives are expanded without editing their entries; documentation is moved
to root and preview-only pictures omitted. Logical paths are lowercase because
the engine's VFS folds their case. No binaries, units, rules, AI files, scripts,
launcher files, full shared feature packs or base archives are included.

## Edition boundaries

**Established — authored metadata and source artifacts.** Gods of War 2 has
six starts and keeps its otherwise generic `Untitled Map` title; the Fixed and
Venom variants are excluded. Metal Heck 2 is Alien's 20 × 20 edition, not the
smaller Venom map. PD Marathon is the authored 18 × 18 `PD_Marathon` map.
Coast to Coast 2 is the multiplayer-pack edition, whose OTA title credits ren;
BMan's different map is excluded. Alien Desert 2 is authored as `Alien Desert II`
and credits C_A_P. John's Pass Redux retains the OTA title `John's Pass`.
Tetra retains `Tetra Threat`. These authored titles are not repaired.

**Established — Supreme Conflict's confusing name.** The publisher's archived
page calls the selected map “Supreme Conflict (OTA Tileset)” and links
`maps/Supreme Conflict Revised.rar`. Its newer TA:K-tileset release instead
links `maps/TAWP_Supreme Conflict.rar`. The
[archived original RAR](https://web.archive.org/web/20160803230800id_/http://taesc.tauniverse.com/maps/Supreme%20Conflict%20Revised.rar)
contains `Supreme Conflict Revised.ufo`, SHA-256
`e50b4280a499159993019d5022a20f15177c583bbb6074cf352aa8a6cac63997`.
This exactly matches the UFO preserved by
[TA Archive's Wotan download](https://www.ta-archive.com/downloads/maps/wotan_Supreme_Conflict_Revised.zip).
TA Archive's separate `Supreme_Conflict_Revised_(r2010).zip` has different
OTA and TNT hashes despite the same dimensions; it is excluded. The selected
map's authored path and `Supreme Conflict Revised` title are preserved.

**Established — Rude Country.** The original publisher credits Wotan. The
[TAF map-version record 2042](https://api.taforever.com/data/mapVersion/2042)
identifies the [preserved UFO](https://content.taforever.com/maps/TAESC_Rude%20Country.ufo)
as `TAESC_Rude Country`. Its OTA names Rude Country and declares ten starts.
The original publisher advertises 36 × 36 while the archive's terrain has an
extra edge strip; authored bytes are preserved.

**Established — Greybeard editions.** Burning Woods V5's archive authors
`maps/burning woods v6.ota`, titled `Burning woods v5`. Metal Picnic V2 retains
its `METAL PICNIC` title and seven starts. Their original Pierre-Be readmes and
Greybeard revision notes are preserved. Winter Canyon's UFO contains the
original default without falling snow; its separately supplied loose OTA
selects optional snowfall and requires a map-weapon pack. The optional OTA is
excluded, preserving the authored default and avoiding global weapon changes.

## Shared feature sprites

**Established — artifact identity.** The required sprite source is
`TA_Features_2013.ccx`, SHA-256
`7f555c233f4fce568d57a5f269561fe5b282989fda7d61ea7aaea0305297962b`,
already preserved with [TA Zero Base](ta-zero-engine.md#evidence-scope-and-sources)
and [Twilight Base Beta 91](twilight-engine.md#evidence-scope-and-artifacts).
It was recovered from the original
[TAF Twilight Base ZIP](https://sjc1.vultrobjects.com/mods/tatw/TAT%20Drop%20in%20b91.zip),
whose SHA-256 is
`52adb7cf335fb03d27c155564c4d786953275170c1fba8234a32a61cc671f319`.
The publisher's “2010 or later” recommendation is satisfied by the inspected
2013 source; an inaccessible 2010 download is not presented as inspected.

**Established — bounded dependency closure.** The maps already carry their
non-retail feature definitions. The shared `ta-features-2013` package therefore
contains only these unchanged sprite banks from the 2013 archive:

| File under `anims/` | Expanded bytes | Required by |
|---|---:|---|
| `custom_zones.gaf` | 146844 | Burning Woods V5, Metal Picnic V2 |
| `megametal.gaf` | 256620 | Burning Woods V5, Metal Picnic V2 |
| `mz.gaf` | 38092 | Burning Woods V5, Metal Picnic V2 |
| `tsuvents.gaf` | 5632 | Metal Picnic V2 |
| `egsnowore.gaf` | 40621 | Winter Canyon |
| `snowtree1.gaf` | 179981 | Winter Canyon |
| `egwvents.gaf` | 18085 | Winter Canyon |

These seven files total 685875 bytes before ZIP compression and package
metadata. The other 584 archive entries are omitted, including all feature
definitions, sounds and unrelated sprites. No shared feature definition can
change retail feature selection. Hot Pot's inspected file references only
retail features, despite its publisher's general feature-pack recommendation;
it needs none of these additional sprites. No dependency is inferred from a
recommendation without an authored reference.

## Acceptance and remaining boundaries

**Established — bounded content checks.** All twenty map pairs parse, expose
a network schema, and have distinct paths absent from the reference retail
install. Every TNT feature record and OTA `FeatureName` reference resolves
against the retail install, its own map package and the seven-sprite dependency.
The feature closure includes destroyed, burnt and reclaimed successors;
required models validate. No bundled map definition differs from a same-name
retail definition. Tetra also carries 986 OTA feature references beyond its
24 TNT records; these were included. The three packages needing third-party
features resolve 8, 10 and 17 non-retail definitions respectively for Burning
Woods V5, Metal Picnic V2 and Winter Canyon. Every sequence requested from the
seven new sprite banks exists.

**Established — bounded engine acceptance.** Each of the twenty ZIPs installed
through `maplibrary.Validator` against only its declared dependency and the
reference retail install. The combined installed roots also passed the retail
feature-preservation check. Each map individually compiled content, entered a
skirmish with one human and one computer player at seed 1, and completed ten
simulation steps. All twenty passed. ZIP hash, size, CRC and identity metadata
also passed for all twenty-one release files. This is startup acceptance, not
a long-match, rendering or every-mod compatibility guarantee.

Existing retail definitions request an absent `empty` tree sequence and carry
a malformed barrier shadow-sequence value. The audit reports those inherited
retail facts separately; packaging changes neither their definitions nor the
maps that reference them. Content closure alone is not a rendered battle test.
Several original TNT files exceed 16 MiB; Seven Arms is the largest at
57303748 bytes, requiring the map library's explicit 64 MiB admission bound.

**Unknown — omitted upstream companion files.** PD Marathon names the
`PD_Marathon` AI profile, absent from its upstream package. Eleven maps name
unit-restriction files that their upstream packages do not include:

| Map | Authored `useonlyunits` reference |
|---|---|
| John's Pass Redux | `John's Pass.tdf` |
| Great Divide 2 | `Great Divide 2.tdf` |
| Metal Heck 2 | `Metal Heck.tdf` |
| Tetra | `Tetra.tdf` |
| PD Marathon | `PD_Divided.tdf` |
| Alien Desert 2 | `Alien Desert II.tdf` |
| Metal Picnic V2 | `Metal Picnic_V2.tdf` |
| Seven Arms | `TESTORS7b.tdf` |
| Winter Canyon | `Winter Canyon.tdf` |
| Burning Woods V5 | `Burning woods v5.tdf` |
| Hot Pot | `Hot Pot.tdf` |

No substitute AI or unit restriction is invented. Nanolathe skirmish uses its
established default-profile fallback and applies `useonlyunits` only in its
campaign path. Historical intended restrictions would require recovering the
author companion files. These authored references remain unchanged. No
packaged default authors a nonempty meteor weapon or supplies COB scripts.

**Unknown — attribution and license limits.** Original readmes, RTF and HTML
notes remain byte-identical in the packages. Metal Heck 2's readme explicitly
permits free redistribution and modification. Other inspected payloads do not
supply a map-specific redistribution license; engine licensing is not asset
licensing. Some payloads include a generic Cavedog copyright notice, which is
preserved without treating it as the custom map's author. The exact author of
Gods of War 2, John's Pass Redux, John's Pond, PD Marathon, Friction and Tetra
is not established by their inspected payloads. The feature archive supplies
no separate readme or per-sprite attribution. Original author documentation
would settle those gaps; no attribution is fabricated.

## Reproducible packaging

**Established — authored preview export (October 5, 2026).** The optional
catalogue previews are PNG exports of the selected packages' own TNT minimaps,
resolved through the reference palette. `tools/map-previews` verifies source
ZIP identities before reading them and uses the frontend chooser's source
crop to omit padding. It preserves the native pixels without resampling or
synthesizing terrain. The twenty previews total 191537 bytes; each is linked
by URL, byte count and SHA-256 in the hosted manifest. The map ZIP hashes and
installed payload sizes remain unchanged. These derived preview images retain
the underlying assets' terms.

`tools/package-maps` builds only an explicitly prepared and inspected root per
package ID. It takes a recipe containing the catalogue identities, selected
OTA paths, direct dependencies, release URL and source provenance. It refuses
unexpected files and symbolic links; it writes sorted, lowercase paths with
fixed timestamps and file modes. A recipe does not approve its inputs: the
engine's `maplibrary.Validator` and battle acceptance remain the publishing
gates. The build outputs the map catalogue and an inventory recording every
file hash, upstream identity, download bytes and exact expanded bytes. Repeating
a build with the same Python/zlib implementation and inputs produces identical
ZIPs. No third-party payload is committed to this repository.

## Inspected source and package inventory

**Established — measured artifacts.** The following exact inputs produced the
first curated release. Download totals include identity and provenance files:
136617491 ZIP bytes and 324102889 expanded bytes for twenty maps and their
shared dependency. A full `inventory.json` accompanies the packaging output
and records every source and output file hash.

| Package | Selected OTA path | ZIP bytes | Expanded bytes | Upstream SHA-256 |
|---|---|---:|---:|---|
| [gods-of-war-2](https://www.ta-archive.com/downloads/maps/tam/Gods_of_War_2_%28tam%29.zip) | `maps/gods of war 2.ota` | 3551960 | 9302401 | `aa63df32f5871530f8c870458f2f316fe0aebb1062339c790242c4c76dc4506f` |
| [great-divide-2](https://www.ta-archive.com/downloads/maps/tam/Great_Divide_2_%28tam%29.zip) | `maps/great divide 2.ota` | 859484 | 1786927 | `83224b4739354f85649f1d5a031b37f35e3d47e4cb2dcc9db353e1e4ad4fd601` |
| [johns-pass-redux](https://www.ta-archive.com/downloads/maps/tam/Johns_Pass_Redux_%28tam%29.zip) | `maps/john's pass redux.ota` | 4356923 | 9269636 | `570ba7a9862be1a9deda5ba5267f6ac002a0b9ba0b79dd614bc9119127013687` |
| [johns-pond](https://www.ta-archive.com/downloads/maps/tam/Johns_Pond_%28tam%29.zip) | `maps/john's pond.ota` | 2494605 | 5669864 | `eea37ff76678136f409a00af4dbd3a284e5adb66d53d678e58da35a84998799e` |
| [metal-heck-2](https://www.ta-archive.com/downloads/maps/Metal_Heck_2_%28r2010%29.zip) | `maps/metal heck 2.ota` | 389090 | 1353858 | `1d9485c9effa510c91942d51f8c5692bc3f619e2e4df1d9a9335e4b046e78473` |
| [pd-marathon](https://www.ta-archive.com/downloads/maps/incoming/PD_Marathon_%28incoming%29.zip) | `maps/pd_marathon.ota` | 3799578 | 8805161 | `3e33ae62dffaf17f271c44c4daea07176888d3792f1c96405e90a0ab1d0213ca` |
| [coast-to-coast-2](https://www.ta-archive.com/downloads/maps/tam/Coast_to_Coast_2_%28tam%29.zip) | `maps/coast to coast 2.ota` | 1220105 | 3782260 | `cf29c9dbbcd867cceb129eb5d4541036cfc70c42a367899639f1c4d1277554e9` |
| [tetra](https://www.ta-archive.com/downloads/maps/tam/Tetra_%28tam%29.zip) | `maps/tetra.ota` | 3270719 | 7370549 | `a3461fcf91459bdcabcd4b7a19e103427435ad8b2c239f96b885593e97ba8947` |
| [friction](https://www.ta-archive.com/downloads/maps/tam/Friction_%28tam%29.zip) | `maps/friction.ota` | 1960358 | 6540690 | `bbe4b92a9773d1f35390623fa696a7bd6f83ace1c523512c722c27173eba5a73` |
| [alien-desert-2](https://www.ta-archive.com/downloads/maps/tam/Alien_Desert_II_%28tam%29.zip) | `maps/alien desert ii.ota` | 1082622 | 2552516 | `7cd5ca14481455abd7d391051a2c522aa34b5de7661f5aa14e46efce3d60a75e` |
| [metal-picnic-v2](https://www.ta-archive.com/downloads/maps/greybeard_Metal_Picnic_V2.zip) | `maps/metal picnic_v2.ota` | 4965578 | 13641692 | `09c1f8ee2861e5901dde2662947d9b9495fe73b205182f6c66c2bcd455b56816` |
| [burning-woods-v5](https://www.ta-archive.com/downloads/maps/greybeard_Burning_Woods_V5.zip) | `maps/burning woods v6.ota` | 5532878 | 14501059 | `70935c658f5f53df58360e88afcfd3a9828900b7e001c497e40bfc3e59d6f140` |
| [hot-pot](https://www.ta-archive.com/downloads/maps/greybeard_Hot_Pot.zip) | `maps/hot pot.ota` | 6645176 | 17574966 | `08799a8a144cbd8d56eacb5203e9f2843719522508461f1c139033ec025720a9` |
| [winter-canyon](https://www.ta-archive.com/downloads/maps/incoming/Winter_Canyon_%28moddb%29.zip) | `maps/winter canyon.ota` | 16479951 | 37373686 | `4889670b1a348c426dcd9c817d6ea19ba15fe73e0186d8150e55b24254130bd1` |
| [supreme-conflict](https://web.archive.org/web/20160803230800id_/http://taesc.tauniverse.com/maps/Supreme%20Conflict%20Revised.rar) | `maps/supreme conflict revised.ota` | 15911850 | 38873343 | `e11c611e7f71b8dd1abe6397ac07db7ed123d8b6c342e9711d6e2acb4ce3ff6d` |
| [seven-arms](https://www.ta-archive.com/downloads/maps/wotan_TAWP_Seven_Arms.zip) | `maps/tawp_seven arms.ota` | 22918874 | 57306252 | `c6c27c25c26bffe9972aca21cd5b9881065755669ce248e57c186293d07bf8f3` |
| [wet-and-dry](https://www.ta-archive.com/downloads/maps/wotan_TAESC_Wet_and_Dry.zip) | `maps/taesc_wet and dry.ota` | 6305563 | 15529190 | `0aa1074dbc3e687dd911ed7b23b72ca226e08c0b294b0631c216b99b933af513` |
| [double-crossing](https://www.ta-archive.com/downloads/maps/wotan_TAWP_Double_Crossing.zip) | `maps/tawp_double crossing.ota` | 9546760 | 18881911 | `eafb57e2f30b6aff5104d4ffa2058285e5e802909b7e851136fd1d747335c1ad` |
| [rude-country](https://content.taforever.com/maps/TAESC_Rude%20Country.ufo) | `maps/taesc_rude country.ota` | 13727643 | 29313940 | `f0c4062c5a352680099d7a291b0a0261f692bb0905d95b0963d87c5801247e96` |
| [the-last-divide](https://www.ta-archive.com/downloads/maps/wotan_TAESC_The_Last_Divide.zip) | `maps/taesc_the last divide.ota` | 11429981 | 23986000 | `95fdf8ebd0e9142448aa1dbb8457fd1cd58305e7c8e387935cca5472fa40e12a` |
| [ta-features-2013](https://sjc1.vultrobjects.com/mods/tatw/TAT%20Drop%20in%20b91.zip) | Seven sprite banks | 167793 | 686988 | `7f555c233f4fce568d57a5f269561fe5b282989fda7d61ea7aaea0305297962b` |

For the feature dependency the input hash identifies the extracted
`TA_Features_2013.ccx` member; the containing Twilight ZIP hash is recorded
above. Source ZIPs and the original RAR/UFO remain separate from these
Nanolathe output packages.

## Second curated batch — October 5, 2026

### Selection evidence and edition boundaries

**Established — historical recommendations, not current usage rankings.**
The [TADG downloads page](https://tadg.tauniverse.com/downloads.htm) lists
Blazters Beach, Center Command, PRO54 Tempest, Sail Here and The Pass II under
its popular third-party map selection. This establishes historical community
curation, not present-day download counts or tournament balance. The inspected
TA Archive multiplayer editions below retain their authored paths and titles;
PRO54 Tempest is titled `Tempest` inside its OTA. Sail Here is the original
four-player edition, not V2 or the Venom revision.

**Established — editorial recommendations.**
[TA Guide's recommended maps](https://taguide.tauniverse.com/downloads.html)
include Capricorn Isles and Micro TA Machines. The latter's original readme
credits RawDeal and describes the armies as pieces in a tabletop game; its
OTA title remains `TA Micro Menace`. Capricorn Isles supplies two large islands
in shallow water. Greybeard's
[publisher page](https://thepack.tauniverse.com/Maps.shtml) supplies Crooked
Creek, a river-crossed crater inspired by a Missouri impact site, and Thin Air,
a lunar landscape with no wind. These are selections for terrain variety;
no popularity measurement is claimed for them.

**Unknown — Beta Tropics edition association.** TADG lists `Beta Tropics`,
but its exact download target could not be inspected. The selected source is
explicitly `Beta Tropics (Coasts)`, distinct from the larger `Beta Tropics` and
later Venom editions. It is an editorial coastal selection; the popular label
is not asserted for this exact edition. Its TNT is 288 × 128 terrain cells;
the OTA size field says 9 × 4, while its description says 10 × 8. Both authored
strings remain unchanged and the catalogue summary avoids a dimension claim.
Recovering TADG's original download target would settle the association.

**Established — measured geometry.** TNT cell dimensions below are read from
the terrain header, rather than inferred from archive labels. Starts count
all authored start-position records, which can exceed the author's recommended
player count. The geometry and metadata are preserved, including the
non-square dimensions of Center Command and The Pass II.

| Map | TNT cells | TNT bytes | Authored starts | OTA size field |
|---|---:|---:|---:|---|
| Beta Tropics (Coasts) | 288 × 128 | 2453593 | 10 | 9 x 4 |
| Blazters Beach | 320 × 320 | 6370816 | 6 | 10 x 10 |
| Center Command | 162 × 360 | 2521568 | 4 | 6 x 12 |
| PRO54 Tempest | 320 × 320 | 7696532 | 10 | 10 x 10 |
| Sail Here | 384 × 384 | 5085172 | 4 | 12 x 12 |
| The Pass II | 224 × 196 | 2629588 | 10 | 7 x 7 |
| Capricorn Isles | 296 × 512 | 17095488 | 10 | 10 x 16 |
| Micro-TA-Machines | 512 × 512 | 6656512 | 8 | 17 x 17 |
| Crooked Creek | 960 × 960 | 28237084 | 10 | 30 x 30 |
| Thin Air | 1120 × 480 | 21112372 | 9 | 35 x 15 |

### Attribution, compatibility and exclusions

**Established — author evidence.** The Blazters Beach OTA description credits
NERDsFist; Center Command's credits Vampiro. Micro-TA-Machines' original
readme credits RawDeal. Greybeard's publisher page and included RTF notes
identify Crooked Creek and Thin Air. Original documentation remains unchanged
in the packages, including generic copyright notices.

**Unknown — missing attribution and licenses.** The inspected payloads do
not establish the individual authors of Beta Tropics (Coasts), PRO54 Tempest,
Sail Here, The Pass II or Capricorn Isles. Generic Cavedog/Humongous copyright
text is not treated as attribution for those custom maps. None of these ten
inspected payloads supplies a map-specific redistribution license. Recovering
original author documentation would settle these gaps; the packages do not
claim the engine's MIT license for their assets.

**Established — map-only scope.** Beta Tropics (Coasts) supplies
`Ai/McnTerra (Naval Series).txt`, and Blazters Beach supplies `ai/Beach.txt`.
These AI overrides are excluded under DESIGN_MODS_MUTATORS §5.6; their OTA
references remain unchanged. Their original AI behavior is consequently not
part of these packages. Center Command names `Devide`, absent from its source.
Nanolathe's existing skirmish default-profile fallback applies when the named
profile is unavailable. No alternate profile is invented.

**Unknown — omitted authored restrictions.** The following OTA references
have no corresponding file in their source archives. They remain unchanged;
skirmish does not apply the campaign-only `useonlyunits` setting. Recovering
the author's companion files would settle the intended restrictions.

| Map | Authored `useonlyunits` reference |
|---|---|
| PRO54 Tempest | `TempestB.tdf` |
| The Pass II | `The Pass II.tdf` |
| Capricorn Isles | `Capricorn Isles.tdf` |
| Micro-TA-Machines | `Evad River Confluence.tdf` |
| Crooked Creek | `Crooked Creek.tdf` |
| Thin Air | `Thin Air.tdf` |

**Established — excluded candidate.**
[Epicentro Activo's inspected source](https://www.ta-archive.com/downloads/maps/ta-power_Epicentro_ACtivo.zip)
credits Bardi and authors `EARTHQUAKE` as a recurring map weapon, with a
bundled weapon definition. The original readme explicitly describes those
earthquakes and their weapon-slot conflict risk. The map is excluded because
that required global weapon content exceeds the map-only package contract;
deleting it would alter the authored map. No modified earthquake-free edition
is substituted. This source also needs additional non-retail feature sprites.

### Acceptance and exact package inventory

**Established — bounded engine acceptance.** All ten ZIPs pass hash, size,
CRC and identity checks and install through `maplibrary.Validator` against
the reference retail install alone. Every OTA/TNT byte matches its inspected
source after archive expansion and path case normalization. Each package has
a distinct map path absent from the retail install, a network schema, complete
feature-name closure including successors, and valid referenced models. All
referenced feature definitions come from retail; no shared dependency is
needed and no existing dependency package changes.

Each map individually compiles content and enters the ordinary two-player
skirmish at simulation and CRT seed 1, with one human and one Classic computer.
Ten `Step` calls complete, advancing from tick 0 to tick 9 after the initial
entry step. The combined thirty maps plus the original shared dependency pass
`maplibrary.ValidateRoots`; each new root also passes against all original
installed roots and retail. No cross-batch feature override is introduced.
These checks establish startup acceptance, not long-match or rendering parity.

The inherited retail `empty` tree-sequence warning also appears for Blazters
Beach, Capricorn Isles and Micro-TA-Machines. Thin Air exposes a separate
inherited retail reference to absent `rock05shad` in `anims/moonrocks.gaf`;
a retail-only mount reproduces it. None of the new packages contains feature
or sprite replacements. These presentation gaps are recorded, not repaired
by changing authored assets. Crooked Creek has the largest new TNT at
28237084 bytes, within the map library's 64 MiB bound.

**Established — measured artifacts.** These ten packages total
35770658 download bytes and 99931386 expanded bytes, including identity and
provenance files. Their batch-only recipe, manifest and full file-hash inventory
are separate from the first release; original release ZIPs remain unchanged.
The source ZIPs below are preserved by TA Archive. `original` continues to
mean an unnumbered inspected edition, not an inferred release number.

The combined catalogue totals 172388149 download bytes and 424034275 expanded
bytes for thirty maps and one shared dependency. The ten new authored minimap
PNGs total 70610 bytes; all thirty previews total 262147 bytes. The existing
twenty packages and previews retain their original size and hash identities.

| Package | Selected OTA path | ZIP bytes | Expanded bytes | Upstream SHA-256 |
|---|---|---:|---:|---|
| [beta-tropics-coasts](https://www.ta-archive.com/downloads/maps/tam/Beta_Tropics_Coasts_%28tam%29.zip) | `maps/beta tropics (coasts).ota` | 766654 | 2456789 | `bf22a46806d073aa365de85f5fa04a5837f149fcfe152606f61eea5ea2826e03` |
| [blazters-beach](https://www.ta-archive.com/downloads/maps/tam/Blazters_Beach_%28tam%29.zip) | `maps/blazters beach.ota` | 2549300 | 6373535 | `0e07d5d282f835a7d61546c46949e0f18c9bf526a42151f289159786a9c6e108` |
| [center-command](https://www.ta-archive.com/downloads/maps/tam/Center_Command_%28tam%29.zip) | `maps/center command.ota` | 1184356 | 2524088 | `ee9bf220e5679f039c2eb30ac492fd789280beac7c56753e6c8eb376870ea9cd` |
| [pro54-tempest](https://www.ta-archive.com/downloads/maps/tam/Tempest_%28tam%29.zip) | `maps/tempest.ota` | 2596708 | 7699662 | `1c3a524bdd4b1484c85ae0213d1b731f95554d6ca013d6b7e52a52737e6c83d7` |
| [sail-here](https://www.ta-archive.com/downloads/maps/tam/Sail_Here_%28tam%29.zip) | `maps/sail here.ota` | 1239688 | 5087585 | `3dcc7292558643d6f2edc0674d151884b31b3736e72a2c8cd9ff052a1d9768ee` |
| [the-pass-ii](https://www.ta-archive.com/downloads/maps/tam/The_Pass_II_%28tam%29.zip) | `maps/the pass ii.ota` | 1199139 | 2632609 | `4e2ec608d593649ac12e0a74dad6dcd005e4ad5ae2cac3e13314090975b4f24e` |
| [capricorn-isles](https://www.ta-archive.com/downloads/maps/tam/Capricorn_Isles_%28tam%29.zip) | `maps/capricorn isles.ota` | 5959702 | 17098668 | `adad4074c8c913b431d6b4294ce695f332144d89ed1aedec1080a818fef53005` |
| [micro-ta-machines](https://www.ta-archive.com/downloads/maps/Micro-TA-Machines_%28r2010%29.zip) | `maps/micro-ta-machines.ota` | 2128936 | 6663729 | `b64eb63bb694d509357150e66afbb20929e9bac83b494a35ec05106bb53d56bc` |
| [crooked-creek](https://www.ta-archive.com/downloads/maps/incoming/Crooked_Creek_%28moddb%29.zip) | `maps/crooked creek.ota` | 10616253 | 28272391 | `350fe0ac01336e9ba7e0128e4075690fb5882d65b23566a57d84875d670ca090` |
| [thin-air](https://www.ta-archive.com/downloads/maps/greybeard_Thin_Air.zip) | `maps/thin air.ota` | 7529922 | 21122330 | `375f11632e442420162aea027377376a8ddf1d750dc539bd74a74357e0028cb5` |

## Cavedog bonus maps — October 16, 2026

The maintainer approved hosting the bonus content that installs carry beside
the base game (DESIGN_MODS_MUTATORS §5.7, D20). Four of those archives hold
only a map pair each, so they join this catalogue as ordinary map packages
rather than as an official add-on.

**Established — source identity.** Each package expands one `*.UFO` archive
of the reference install; every file in it is under `maps/`, and the package
holds those OTA/TNT bytes unchanged, paths in lower case. Comet Catcher's OTA
names its mission "Shew Drop"; the catalogue uses the file's name, which the
map chooser lists.

**Unknown — authorship and release.** Whether Cavedog published these four
as downloads, and when, is not established; "PC Games'" in one name suggests
a magazine release. A mirror of Cavedog's download pages would settle it.

**Established — bounded engine acceptance.** All four ZIPs install through
`maplibrary.InstallValidator` against an install holding every reference
archive except the four sources. Comet Catcher is refused there with
`ErrMapInInstall`, because The Core Contingency's `ccmaps.ccx` already
supplies `maps/comet catcher.ota`; it installs only for an install without
that expansion. Each is refused the same way against the reference install,
whose folder holds the sources. Every referenced feature comes from retail,
and no shared dependency is needed. The four total 5541937 download bytes and
16777157 expanded bytes; their minimap PNGs total 26903 bytes.

| Package | Selected OTA path | ZIP bytes | Expanded bytes | Source archive | Source SHA-256 |
|---|---|---:|---:|---|---|
| comet-catcher | `maps/comet catcher.ota` | 1876725 | 5379185 | `Cometctr.ufo` | `b7055488519dc028acafa09d71cdf767c0bc1ba8faf21e0aa637408403619123` |
| luschinfloggen | `maps/luschinfloggen.ota` | 1018945 | 3111601 | `floggen.ufo` | `0e88714622e3bd45f1d1747c81f84ba72f7e3a0c5d476a150f15535f9a3bc978` |
| mounds-of-mars | `maps/mounds of mars.ota` | 1509919 | 4566972 | `Mndsmars.ufo` | `69be3671a251f0b2c9940e502226c220c389cf49f7ae8bef788f31eae9e10704` |
| pc-games-evad-river-delta | `maps/pc games' evad river delta.ota` | 1136348 | 3719399 | `Evadrivd.ufo` | `4c825aff22668f7f7e55438be27b8fdff1d09ab6f53619974d82493bda5a274a` |

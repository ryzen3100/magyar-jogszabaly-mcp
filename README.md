# Magyar Jogszabály MCP Szerver

**A Magyar Közlöny alternatívája a mesterséges intelligencia korában.**

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

Keresd meg **72 932 magyar jogszabály** rendelkezéseit — a Ptk.-tól és az Mt.-től a GDPR végrehajtási törvényéig és a Btk.-ig — közvetlenül a Claude-ban, a Cursorban vagy bármely MCP-kompatibilis kliensben.

Ha jogtechnológiai megoldásokat fejlesztesz, megfelelőségi eszközöket készítesz vagy magyar jogi kutatást végzel, megbízható referencia-adatbázisként használhatod.

Az [Ansvar Systems](https://ansvar.eu) Hungarian Law MCP-szerverére épül, és magyar hivatkozás-feldolgozóval, uniós megfeleltetésekkel, valamint KKV-k számára készült jogi skillrendszerrel egészül ki.

---

## Gyors indulás

### Futtatás saját LAN-on Docker Compose használatával

A szerver helyi hálózaton való futtatásra készült: nincs nyilvános végpont, és nincs szükség routeres porttovábbításra. Alapértelmezés szerint az MCP-végpont nem használ külön alkalmazásszintű hitelesítést; a hozzáférést az OpenMediaVault tűzfala korlátozza a helyi hálózatra.

Indítsd el a szolgáltatást:

```bash
docker compose pull
docker compose up -d
docker compose ps
```

A szolgáltatás MCP-végpontja a helyi hálózaton:

```text
http://openmediavault.local:3000/mcp
```

A Docker-konténerkép az adatbázist is tartalmazza. Ha új GHCR-konténerképet szeretnél használni, futtasd újra a `docker compose pull && docker compose up -d` parancsot; a meglévő adatkötet automatikusan frissül, ha a konténerképben lévő adatbázis megváltozott. A hostgép tűzfalán a 3000-es portot csak a LAN felől engedélyezd.

### MCP-kliensek konfigurálása

A Docker Compose használatával futó szerverhez az alábbi Streamable HTTP-végponton csatlakozhatsz.

**Pi és más JSON-alapú konfigurációt használó kliensek** — `~/.config/mcp/mcp.json`:

```json
{
  "mcpServers": {
    "magyar-jogszabaly": {
      "url": "http://openmediavault.local:3000/mcp"
    }
  }
}
```

Az `openmediavault.local` helyére a szervert futtató gép hostnevét vagy IP-címét írd.

**Claude Code:**

```bash
claude mcp add --transport http --scope user magyar-jogszabaly http://openmediavault.local:3000/mcp
```

A `--scope user` a beállítást felhasználói szinten menti; hagyd el, ha csak az aktuális projekthez szeretnéd hozzáadni.

---

## Példakérdések

A csatlakozás után természetes nyelven is felteheted a kérdéseidet:

- *"Keresés 'adatvédelem' — milyen kötelezettségeket állapít meg a GDPR végrehajtási törvény?"*
- *"Hatályban van-e a Büntető Törvénykönyv 370. §-a?"*
- *"Hány nap szabadság jár egy 42 éves munkavállalónak?"*
- *"Melyik uniós irányelvet ültette át a Ptk. fogyasztóvédelmi fejezete?"*
- *"Ellenőrizd a hivatkozást: 2012. évi I. törvény 116. §"*
- *"Milyen engedély kell kávézó nyitásához?"*
- *"A partnerem 3 hónapja nem fizet, mit tegyek?"*

### Keresési tippek

A teljes szövegű keresés a **magyar nyelvű** jogszabályi szövegen fut, szótövezés
(stemming) nélkül, ezért a keresőszavakra az alábbiak igazak:

- **Magyar alanyesetű kulcsszavakkal** a legjobb a találat: a `szabadság munkavállaló`
  kifejezés pontosabb, mint a `szabadságot a munkavállalónak`. A természetes nyelvű
  kérdések is működnek (a központozás és a gyakori kötőszavak — *hány, milyen, kell,
  hogy* — automatikusan kiszűrésre kerülnek, minden szó prefix-kereséssel és egy
  egyszerű szótő-approximációval is próbálkozik), de a 2–4 kulcsszó pontosabb találatot ad.
- Az uniós jogi aktusok címei **angolul** vannak tárolva — az `search_eu_implementations`
  keresőben angolul érdemes keresni.
- A `search_legislation` támogatja a `AND` / `OR` / `NOT` logikai operátorokat és a
  szóvégi `*` prefix-keresést is.

---

## Az adatbázis tartalma

| Kategória | Darabszám | Részletek |
|-----------|------|-----------|
| **Jogszabályok** | 72 932 | Az njt.hu-ról származó teljes magyar joganyag |
| **Rendelkezések** | 1 113 243 | FTS5-alapú teljes szövegű keresés |
| **Fogalom-definíciók** | 25 109 | A szabályozási fogalmak kinyert definíciói |
| **Uniós kereszthivatkozások** | 92 | Irányelvek és rendeletek magyar jogszabályokhoz kapcsolva |
| **Adatbázis mérete** | 1,6 GB | Optimalizált SQLite-adatbázis |
| **Frissítés** | Napi ellenőrzés | Az új adatok új konténerképpel jelennek meg |

**Csak ellenőrzött adatok** — a hivatkozások mind az njt.hu és a Magyar Közlöny hivatalos forrásaira mutatnak. A jogszabályszövegeket nem LLM generálja.

---

## Elérhető eszközök (13)

### Alapvető jogi kutatási eszközök (8)

| Eszköz | Leírás |
|--------|--------|
| `search_legislation` | FTS5-alapú teljes szövegű keresés 1 113 243 rendelkezésben, BM25 szerinti rangsorolással |
| `get_provision` | Konkrét rendelkezés lekérése a jogszabály azonosítója és a szakaszszám alapján |
| `validate_citation` | Hivatkozás ellenőrzése az adatbázisban; a magyar formátumokat is támogatja (`"2012. évi I. törvény 116. §"`) |
| `build_legal_stance` | Több jogszabály hivatkozásainak összesítése egy jogi témához |
| `format_citation` | Hivatkozások formázása a magyar jogi gyakorlat szerint (teljes és pinpoint hivatkozás) |
| `check_currency` | A jogszabály hatályának ellenőrzése (hatályos, módosított vagy hatályon kívül helyezett) |
| `list_sources` | Az elérhető jogszabályok listázása metaadatokkal |
| `about` | Szerverinformációk és adatbázis-statisztikák |

### Uniós jogi eszközök (5)

| Eszköz | Leírás |
|--------|--------|
| `get_eu_basis` | Az adott magyar jogszabály alapjául szolgáló uniós irányelvek és rendeletek lekérése |
| `get_hungarian_implementations` | Egy adott uniós jogi aktushoz kapcsolódó magyar végrehajtási jogszabályok keresése |
| `search_eu_implementations` | Uniós dokumentumok keresése a magyar átültetési vagy végrehajtási számuk alapján |
| `get_provision_eu_basis` | Egy konkrét rendelkezéshez kapcsolódó uniós jogi hivatkozások lekérése |
| `validate_eu_compliance` | A magyar jogszabályok uniós irányelvekkel való megfelelőségének ellenőrzése |

---

## KKV-k jogi skillkönyvtára

Ez az MCP-szerver együtt használható a **[KKV Jogi Csapat](https://github.com/gergototh1/kkv-jogi-csapat)** skillrendszerével. A 11 Claude Code-skill a magyar KKV-k leggyakoribb jogi kérdéseire ad gyakorlati iránymutatást.

### Hogyan működik

```
Felhasználó kérdése
  → kkv-jogi-router (a kérdés besorolása 10 szakterület egyikébe)
    → Szakterületi skill (pl. kkv-munkajog)
      → MCP-eszközök hívása (get_provision, check_currency, validate_citation)
        → Pontos jogszabályszöveg az adatbázisból
          → Strukturált válasz (kockázati szint, teendők és jogi nyilatkozat)
```

### Elérhető skillek

| Skill | Terület |
|-------|---------|
| **kkv-jogi-router** | Orchestrator — a kérdést automatikusan a megfelelő skillhez irányítja |
| **kkv-munkajog** | Munkaszerződés, felmondás, szabadság, túlóra és home office (Mt.) |
| **kkv-ado** | ÁFA, TAO, KIVA, KATA, számlázás és adóellenőrzés (Art. + ÁFA tv.) |
| **kkv-gdpr** | Adatkezelési tájékoztató, DPIA, adatvédelmi incidens és cookie-k (Infotv. + GDPR) |
| **kkv-szerzodes** | ÁSZF-ek ellenőrzése, kellékszavatosság, NDA-k és szabadúszói szerződések (Ptk.) |
| **kkv-cegjog** | Kft./Bt. alapítása, taggyűlés, törzstőke és EV→Kft. átmenet (Ctv.) |
| **kkv-fogyasztovedelmi** | Webshop, elállási jog, jótállás és online piacterek (Fgytv.) |
| **kkv-koveteleskezeles** | Fizetési meghagyásos eljárás, végrehajtás és késedelmi kamat (Fmhtv. + Vht.) |
| **kkv-szellemi-tulajdon** | Védjegy, szerzői jog, szoftverek szellemi tulajdona és domainviták (Szjt. + Vt.) |
| **kkv-ingatlan** | Székhelyszolgáltatás, bérleti szerződés, telephely (Ptk. + Ctv.) |
| **kkv-engedelyek** | Működési engedély, vendéglátás, NTAK, HACCP (Kertv.) |

**Telepítés:** [github.com/gergototh1/kkv-jogi-csapat](https://github.com/gergototh1/kkv-jogi-csapat)

---

## Adatforrások és az adatok frissessége

A tartalom hiteles magyar jogi forrásokból származik:

- **[njt.hu](https://njt.hu/)** — Nemzeti Jogszabálytár, a hivatalos, konszolidált magyar jogszabály-adatbázis
- **[Magyar Közlöny](https://magyarkozlony.hu/)** — a jogszabályok hivatalos kihirdetésének forrása
- **[EUR-Lex](https://eur-lex.europa.eu/)** — az Európai Unió hivatalos jogi adatbázisa (csak metaadatok)

---

## Fontos jogi nyilatkozatok

### Jogi tanácsadás

> **EZ AZ ESZKÖZ NEM NYÚJT JOGI TANÁCSADÁST**
>
> A jogszabályszövegek az njt.hu és a Magyar Közlöny hivatalos kiadványaiból származnak. Ennek ellenére:
> - Ez **kutatási eszköz**; nem helyettesíti a szakszerű jogi tanácsadást.
> - **A bírósági beadványokban használt kritikus hivatkozásokat ellenőrizd** az elsődleges forrásokban.
> - **Az uniós kereszthivatkozások** a magyar jogszabályszövegből származnak, és nem az EUR-Lex teljes szövegéből.
> - **Szakmai célú hivatkozás előtt mindig ellenőrizd** a jogszabály hatályosságát az njt.hu-n.

### Titoktartás

A lekérdezések az MCP-protokollon keresztül jutnak el a szerverhez. Bizalmas vagy ügyvédi titkot érintő ügyekben helyi telepítést használj.

---

## Fejlesztés

A szerver Go-ban íródott — futtatásához már nincs szükség Node.js-re. Az építéshez Go 1.26 vagy újabb verzió szükséges, és cgo nélkül fordul.

### Telepítés

```bash
git clone https://github.com/ryzen3100/magyar-jogszabaly-mcp
cd magyar-jogszabaly-mcp
go build ./cmd/hungarian-law-mcp
```

vagy a közzétett Go modulból (release címke megjelenése után):

```bash
go install github.com/ryzen3100/magyar-jogszabaly-mcp/v2/cmd/hungarian-law-mcp@latest
```

### Futtatás

```bash
hungarian-law-mcp                   # stdio MCP (alapértelmezett)
HOST=127.0.0.1 PORT=3000 hungarian-law-mcp serve   # Streamable HTTP a 3000-es porton
```

A `PORT` alapértelmezett értéke `3000`, a `HOST`-é `127.0.0.1` (loopback). Az adatbázis helye alapértelmezés szerint `data/database.db`, a futtatható állományhoz képest feloldva — mivel a `go run` ideiglenes könyvtárba fordít, fejlesztési futtatáshoz állítsd be a `HUNGARIAN_LAW_DB_PATH` környezeti változót, pl. `HUNGARIAN_LAW_DB_PATH=data/database.db go run ./cmd/hungarian-law-mcp`.

### Adatbázis-kezelés

```bash
go run ./cmd/build-db       # SQLite-adatbázis újraépítése a seed-fájlokból
```

### Az adatbázis biztonságos kézi frissítése

A szolgáltatás futás közben nem tölt le új adatokat az njt.hu-ról. A frissítést külön Git-ágon végezd, hogy a letöltött és feldolgozott adatokat át tudd tekinteni:

```bash
git switch -c data/update-YYYY-MM-DD
go run ./cmd/ingest -full -refresh-discovery
go run ./cmd/build-db

go vet ./...
go test ./...
go run ./cmd/check-updates
```

Az `ingest` szkript a hivatalos `njt.hu` oldalról frissíti a seed-fájlokat. A `data/census.json` és a `sources.yml` fájlokat nem módosítja automatikusan; a frissítés eredménye alapján nézd át, és szükség esetén kézzel módosítsd őket:

```bash
git status --short
git diff -- data/seed data/census.json sources.yml
```

A `data/database.db` nem kerül be a Gitbe; a Docker-konténerkép a seed-fájlokból építi fel az adatbázist. Ha a változások rendben vannak:

```bash
git add data/seed data/census.json sources.yml
git commit -m "Update Hungarian legal database"
git push -u origin HEAD
```

A GHCR-konténerkép sikeres elkészítése és a smoke teszt lefutása után az OpenMediaVaultot futtató gépen frissítsd a szolgáltatást:

```bash
docker compose pull
docker compose up -d
docker compose ps
```

A `go run ./cmd/check-updates` csak a helyi adatbázis korát, rekordszámát és az `njt.hu` elérhetőségét ellenőrzi; nem helyettesíti a teljes NJT-adatállomány újbóli betöltését. Az adatbetöltést ne az éles konténer shelljéből és ne MCP-eszközön keresztül futtasd.

---

## Az Ansvar-fork módosításai

Ez a fork az [Ansvar Systems Hungarian Law MCP](https://github.com/Ansvar-Systems/Hungarian-law-mcp) szerverre épül, és a következő módosításokat tartalmazza:

- **Magyar hivatkozás-feldolgozó** — a `validate_citation("2012. évi I. törvény 116. §")` formátum felismerése
- **Ptk.-s, kettőspontos szakaszjelölés** — a `6:272. §` formátum kezelése
- **Magyar nyelvű, normalizált kimenet** — `"doc.title NNN. §"` formátum
- **Uniós megfeleltetési adatok** — 14 irányelv és rendelet (GDPR, Consumer Rights, ePrivacy, VAT stb.)
- **Adatbázis-lekérdezés a `format_citation` eszközben** — a teljes cím feloldása az adatbázisból
- **KKV-k jogi skillkönyvtára** — 11 KKV-jogi skill magyar kis- és középvállalkozások számára

---

## Licenc

Apache License 2.0. A részleteket lásd a [LICENSE](./LICENSE) fájlban.

### Adatlicencek

- **Jogszabályok:** Magyarország Kormánya / njt.hu (közkincs)
- **Uniós metaadatok:** EUR-Lex (EU-s közkincs)

---

## Az eredeti projekt

Ez a projekt az [Ansvar Systems](https://ansvar.eu) (Stockholm, Svédország) Hungarian Law MCP-szerverének forkja.

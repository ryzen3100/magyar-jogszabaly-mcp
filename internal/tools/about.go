// about — Server metadata, dataset statistics, and provenance.
// Port of src/tools/about.ts.

package tools

import (
	"context"
	"database/sql"
	"errors"

	"github.com/ryzen3100/magyar-jogszabaly-mcp/v2/internal/store"
)

// aboutDescription is the long Hungarian server description, byte-identical
// to the concatenated string literal in src/tools/about.ts:33-53.
const aboutDescription = "Magyar jogszabály-adatbázis a Nemzeti Jogszabálytár (njt.hu) hivatalos forrásából, " +
	"Model Context Protocol (MCP) interfészen keresztül. " +
	"Az adatbázis több mint 4 300 hatályos és hatályon kívüli magyar jogszabályt tartalmaz, " +
	"130 000+ szakasz-szintű bekezdéssel és 5 000+ jogszabályi definícióval. " +
	"A lefedett jogterületek között szerepel a teljes Polgári Törvénykönyv (Ptk. — 2013. évi V. tv.), " +
	"az információs önrendelkezési törvény (Infotv. — 2011. évi CXII. tv.), " +
	"a Munka Törvénykönyve (Mt. — 2012. évi I. tv.), a Büntető Törvénykönyv (Btk. — 2012. évi C. tv.), " +
	"a fogyasztóvédelmi törvény (Fgytv.), az elektronikus kereskedelmi törvény (Eker. tv.), " +
	"a cégtörvény (Ctv.), valamint az összes további, az njt.hu-n elérhető jogszabály. " +
	"Az EU-s kereszthivatkozási rendszer 50+ irányelvet és rendeletet követ nyomon " +
	"(GDPR 2016/679, NIS2 2022/2555, e-Privacy, Kereskedelmi titkok irányelve 2016/943 stb.), " +
	"feltüntetve melyik magyar jogszabály melyik EU-s jogforrást ülteti át. " +
	"A keresés BM25 rangsorolású teljes szöveges kereséssel (FTS5) működik, " +
	"támogatja a pontos kifejezés keresést, boolean operátorokat és prefix wildcard-okat. " +
	"Elérhető funkciók: jogszabályszöveg lekérdezése szakaszszámra, hivatkozás-validálás " +
	"(hallucinációmentes ellenőrzés), hatályossági státusz vizsgálat, EU-megfelelőségi ellenőrzés, " +
	"valamint átfogó jogi álláspont felépítése több jogszabály egyidejű keresésével. " +
	"Az adatbázis naponta frissül az njt.hu hivatalos forrásából. " +
	"Forrás: Magyar Közlöny / Nemzeti Jogszabálytár. " +
	"Figyelmeztetés: ez kutatási eszköz, nem jogi tanácsadás — " +
	"kritikus hivatkozásokat mindig ellenőrizze a hivatalos forráson (njt.hu)."

// aboutStats mirrors the TS stats record: documents/provisions/definitions
// always present, the two EU keys only when the eu_references count is > 0.
// ponytail: omitempty also drops eu_documents when it is 0 while
// eu_references > 0 (references without an eu_documents table) where TS would
// emit 0; upgrade path is a custom MarshalJSON on aboutStats.
type aboutStats struct {
	Documents    int  `json:"documents"`
	Provisions   int  `json:"provisions"`
	Definitions  int  `json:"definitions"`
	EUDocuments  *int `json:"eu_documents,omitempty"`
	EUReferences *int `json:"eu_references,omitempty"`
}

type aboutDataSource struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Authority string `json:"authority"`
}

type aboutFreshness struct {
	DatabaseBuilt string `json:"database_built"`
}

type aboutNetwork struct {
	Name      string `json:"name"`
	OpenLaw   string `json:"open_law"`
	Directory string `json:"directory"`
}

// aboutResult mirrors the TS about return object; field order = TS insertion
// order. Note data_sources uses 'Nemzeti Jogszabalytar (NJT)' without accents
// — deliberately different from list_sources.
type aboutResult struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Jurisdiction string            `json:"jurisdiction"`
	Description  string            `json:"description"`
	Stats        aboutStats        `json:"stats"`
	DataSources  []aboutDataSource `json:"data_sources"`
	Freshness    aboutFreshness    `json:"freshness"`
	Disclaimer   string            `json:"disclaimer"`
	Network      aboutNetwork      `json:"network"`
}

// errAboutNotConfigured answers an about call when no AboutContext is
// configured; the dispatcher surfaces it verbatim as the tool-result text.
var errAboutNotConfigured = errors.New("about tool not configured")

// GetAbout implements the about MCP tool.
func GetAbout(ctx context.Context, db *sql.DB, about *AboutContext) (any, ResponseMetadata, error) {
	if about == nil {
		return nil, ResponseMetadata{}, errAboutNotConfigured
	}

	euRefs := store.CachedCount(ctx, db, "SELECT COUNT(*) as count FROM eu_references")

	stats := aboutStats{
		Documents:   store.CachedCount(ctx, db, "SELECT COUNT(*) as count FROM legal_documents"),
		Provisions:  store.CachedCount(ctx, db, "SELECT COUNT(*) as count FROM legal_provisions"),
		Definitions: store.CachedCount(ctx, db, "SELECT COUNT(*) as count FROM definitions"),
	}
	if euRefs > 0 {
		stats.EUDocuments = new(store.CachedCount(ctx, db, "SELECT COUNT(*) as count FROM eu_documents"))
		stats.EUReferences = &euRefs
	}

	return aboutResult{
		Name:         "Hungarian Law MCP",
		Version:      about.Version,
		Jurisdiction: "HU",
		Description:  aboutDescription,
		Stats:        stats,
		DataSources: []aboutDataSource{
			{
				Name:      "Nemzeti Jogszabalytar (NJT)",
				URL:       "https://njt.hu",
				Authority: "Ministry of Justice",
			},
		},
		Freshness:  aboutFreshness{DatabaseBuilt: about.DBBuilt},
		Disclaimer: "This is a research tool, not legal advice. Verify critical citations against official sources.",
		Network: aboutNetwork{
			Name:      "Ansvar MCP Network",
			OpenLaw:   "https://ansvar.eu/open-law",
			Directory: "https://ansvar.ai/mcp",
		},
	}, GenerateResponseMetadata(ctx, db), nil
}

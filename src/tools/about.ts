/**
 * about — Server metadata, dataset statistics, and provenance.
 */

import type Database from '@ansvar/mcp-sqlite';

export interface AboutContext {
  version: string;
  fingerprint: string;
  dbBuilt: string;
}

function safeCount(db: InstanceType<typeof Database>, sql: string): number {
  try {
    const row = db.prepare(sql).get() as { count: number } | undefined;
    return row ? Number(row.count) : 0;
  } catch {
    return 0;
  }
}

export function getAbout(db: InstanceType<typeof Database>, context: AboutContext) {

  const euRefs = safeCount(db, 'SELECT COUNT(*) as count FROM eu_references');

  const stats: Record<string, number> = {
    documents: safeCount(db, 'SELECT COUNT(*) as count FROM legal_documents'),
    provisions: safeCount(db, 'SELECT COUNT(*) as count FROM legal_provisions'),
    definitions: safeCount(db, 'SELECT COUNT(*) as count FROM definitions'),
  };

  if (euRefs > 0) {
    stats.eu_documents = safeCount(db, 'SELECT COUNT(*) as count FROM eu_documents');
    stats.eu_references = euRefs;
  }

  return {
    name: 'Hungarian Law MCP',
    version: context.version,
    jurisdiction: 'HU',
    description:
      'Magyar jogszabály-adatbázis a Nemzeti Jogszabálytár (njt.hu) hivatalos forrásából, ' +
      'Model Context Protocol (MCP) interfészen keresztül. ' +
      'Az adatbázis több mint 4 300 hatályos és hatályon kívüli magyar jogszabályt tartalmaz, ' +
      '130 000+ szakasz-szintű bekezdéssel és 5 000+ jogszabályi definícióval. ' +
      'A lefedett jogterületek között szerepel a teljes Polgári Törvénykönyv (Ptk. — 2013. évi V. tv.), ' +
      'az információs önrendelkezési törvény (Infotv. — 2011. évi CXII. tv.), ' +
      'a Munka Törvénykönyve (Mt. — 2012. évi I. tv.), a Büntető Törvénykönyv (Btk. — 2012. évi C. tv.), ' +
      'a fogyasztóvédelmi törvény (Fgytv.), az elektronikus kereskedelmi törvény (Eker. tv.), ' +
      'a cégtörvény (Ctv.), valamint az összes további, az njt.hu-n elérhető jogszabály. ' +
      'Az EU-s kereszthivatkozási rendszer 50+ irányelvet és rendeletet követ nyomon ' +
      '(GDPR 2016/679, NIS2 2022/2555, e-Privacy, Kereskedelmi titkok irányelve 2016/943 stb.), ' +
      'feltüntetve melyik magyar jogszabály melyik EU-s jogforrást ülteti át. ' +
      'A keresés BM25 rangsorolású teljes szöveges kereséssel (FTS5) működik, ' +
      'támogatja a pontos kifejezés keresést, boolean operátorokat és prefix wildcard-okat. ' +
      'Elérhető funkciók: jogszabályszöveg lekérdezése szakaszszámra, hivatkozás-validálás ' +
      '(hallucinációmentes ellenőrzés), hatályossági státusz vizsgálat, EU-megfelelőségi ellenőrzés, ' +
      'valamint átfogó jogi álláspont felépítése több jogszabály egyidejű keresésével. ' +
      'Az adatbázis naponta frissül az njt.hu hivatalos forrásából. ' +
      'Forrás: Magyar Közlöny / Nemzeti Jogszabálytár. ' +
      'Figyelmeztetés: ez kutatási eszköz, nem jogi tanácsadás — kritikus hivatkozásokat mindig ellenőrizze a hivatalos forráson (njt.hu).',
    stats,
    data_sources: [
      {
        name: 'Nemzeti Jogszabalytar (NJT)',
        url: 'https://njt.hu',
        authority: 'Ministry of Justice',
      },
    ],
    freshness: {
      database_built: context.dbBuilt,
    },
    disclaimer:
      'This is a research tool, not legal advice. Verify critical citations against official sources.',
    network: {
      name: 'Ansvar MCP Network',
      open_law: 'https://ansvar.eu/open-law',
      directory: 'https://ansvar.ai/mcp',
    },
  };
}

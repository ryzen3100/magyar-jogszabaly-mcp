/**
 * list_sources — Return provenance metadata for all data sources.
 */

import type Database from '@ansvar/mcp-sqlite';
import { readDbMetadata } from '../capabilities.js';
import { generateResponseMetadata, safeCount, type ToolResponse } from '../utils/metadata.js';

interface SourceInfo {
  name: string;
  authority: string;
  url: string;
  license: string;
  coverage: string;
  languages: string[];
}

interface ListSourcesResult {
  sources: SourceInfo[];
  database: {
    tier: string;
    schema_version: string;
    built_at?: string;
    document_count: number;
    provision_count: number;
  };
}

export async function listSources(
  db: InstanceType<typeof Database>,
): Promise<ToolResponse<ListSourcesResult>> {
  const meta = readDbMetadata(db);

  return {
    results: {
      sources: [
        {
          name: 'Nemzeti Jogszabálytár (National Legislation Database)',
          authority: 'Magyar Közlöny (Hungarian Official Gazette)',
          url: 'https://njt.hu',
          license: 'Official legal text publication (see portal terms at njt.hu)',
          coverage:
            'Curated set of key Hungarian statutes covering data protection, cybersecurity, ' +
            'electronic commerce, telecommunications, public procurement, trade secrets, ' +
            'trust services, and criminal cybercrime provisions',
          languages: ['hu', 'en'],
        },
      ],
      database: {
        tier: meta.tier,
        schema_version: meta.schema_version,
        built_at: meta.built_at,
        document_count: safeCount(db, 'SELECT COUNT(*) as count FROM legal_documents'),
        provision_count: safeCount(db, 'SELECT COUNT(*) as count FROM legal_provisions'),
      },
    },
    _metadata: generateResponseMetadata(db),
  };
}
